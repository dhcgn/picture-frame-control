// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dhcgn/picture-frame-control/internal/i18n"
	"github.com/dhcgn/picture-frame-control/internal/immich"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

// ImmichService connects to Immich and lets the user choose which albums and
// tags go onto which frame (I-01..I-06, S-01).
type ImmichService struct {
	store *store.Store
}

func (s *ImmichService) client() (*immich.Client, error) {
	server := s.store.Config().ImmichServer
	if server == "" {
		return nil, errors.New(i18n.T("Immich ist noch nicht verbunden. Server und API-Key in den Einstellungen eintragen",
			"Immich is not connected yet. Enter server and API key in the settings"))
	}
	return immich.New(server, s.store.ImmichAPIKey())
}

// Connection is the stored connection, without the key itself.
type Connection struct {
	Server string `json:"server"`
	HasKey bool   `json:"hasKey"`
}

func (s *ImmichService) Connection() Connection {
	return Connection{Server: s.store.Config().ImmichServer, HasKey: s.store.ImmichAPIKey() != ""}
}

// ServerInfo describes a working connection.
type ServerInfo struct {
	Version string `json:"version"`
	User    string `json:"user"`
}

func check(ctx context.Context, c *immich.Client) (ServerInfo, error) {
	v, err := c.Version(ctx)
	if err != nil {
		return ServerInfo{}, err
	}
	if !v.AtLeast(3, 2) {
		return ServerInfo{}, fmt.Errorf(i18n.T("Immich %s ist zu alt. Die App braucht mindestens 3.2", "Immich %s is too old. The app needs at least 3.2"), v)
	}
	u, err := c.Me(ctx)
	if err != nil {
		return ServerInfo{}, err
	}
	return ServerInfo{Version: v.String(), User: cmp.Or(u.Name, u.Email)}, nil
}

// Connect tests server and key and saves them if they work. An empty key
// keeps the stored one, so the address can change without retyping it.
func (s *ImmichService) Connect(ctx context.Context, server, apiKey string) (ServerInfo, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if apiKey == "" {
		apiKey = s.store.ImmichAPIKey()
	}
	c, err := immich.New(server, apiKey)
	if err != nil {
		return ServerInfo{}, err
	}
	info, err := check(ctx, c)
	if err != nil {
		return ServerInfo{}, err
	}
	if err := s.store.SetImmichAPIKey(strings.TrimSpace(apiKey)); err != nil {
		return ServerInfo{}, err
	}
	return info, s.store.Update(func(cfg *store.Config) error { cfg.ImmichServer = server; return nil })
}

// Status checks the stored connection.
func (s *ImmichService) Status(ctx context.Context) (ServerInfo, error) {
	c, err := s.client()
	if err != nil {
		return ServerInfo{}, err
	}
	return check(ctx, c)
}

// Source is an album or tag that can be mirrored onto frames.
type Source struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // "album" or "tag"
	Name   string `json:"name"`
	Path   string `json:"path"`  // tags: full path, e.g. "reisen/ostsee"; albums: the name
	Depth  int    `json:"depth"` // tags: nesting level, 0 at the top
	Shared bool   `json:"shared"`
	Count  int    `json:"count"` // images and videos a frame would get; -1 if unknown
}

// Sources lists all albums, then all tags in tree order, each with how many
// images and videos it holds by the same rules the sync uses.
func (s *ImmichService) Sources(ctx context.Context) ([]Source, error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	albums, err := c.Albums(ctx)
	if err != nil {
		return nil, err
	}
	tags, err := c.Tags(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(albums, func(a, b immich.Album) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	slices.SortFunc(tags, func(a, b immich.Tag) int { return cmp.Compare(strings.ToLower(a.Value), strings.ToLower(b.Value)) })

	out := make([]Source, 0, len(albums)+len(tags))
	for _, a := range albums {
		out = append(out, Source{ID: a.ID, Kind: "album", Name: a.Name, Path: a.Name, Shared: a.Shared, Count: -1})
	}
	for _, t := range tags {
		out = append(out, Source{ID: t.ID, Kind: "tag", Name: t.Name, Path: t.Value, Depth: strings.Count(t.Value, "/"), Count: -1})
	}

	// One small statistics request per source, a few at a time.
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range out {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			var n int
			var err error
			if out[i].Kind == "album" {
				n, err = c.Count(ctx, []string{out[i].ID}, nil)
			} else {
				n, err = c.Count(ctx, nil, []string{out[i].ID})
			}
			if err == nil {
				out[i].Count = n
			}
		})
	}
	wg.Wait()
	return out, nil
}

// SetSources saves which albums and tags a frame mirrors.
func (s *ImmichService) SetSources(frameID string, albums, tags []string) error {
	return s.store.UpdateFrame(frameID, func(f *store.Frame) {
		f.Sources = store.Sources{Albums: slices.Clone(albums), Tags: slices.Clone(tags)}
	})
}

// Selection is what a set of albums and tags adds up to.
type Selection struct {
	Images int `json:"images"`
	Videos int `json:"videos"`
}

// Preview counts the images and videos the albums and tags hold together,
// each asset once however many of them it is in.
func (s *ImmichService) Preview(ctx context.Context, albums, tags []string) (Selection, error) {
	if len(albums)+len(tags) == 0 {
		return Selection{}, nil
	}
	c, err := s.client()
	if err != nil {
		return Selection{}, err
	}
	var sel Selection
	if sel.Images, err = c.Count(ctx, albums, tags, immich.Image); err != nil {
		return sel, err
	}
	sel.Videos, err = c.Count(ctx, albums, tags, immich.Video)
	return sel, err
}
