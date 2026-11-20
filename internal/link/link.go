// SPDX-License-Identifier: GPL-3.0-or-later

// Package link opens connections to Frameo frames: directly over the local
// network when the frame is found there, otherwise through Frameo's relay.
//
// Adapted from unframeo's cmd/unframeo/main.go (connect, dialGrid,
// connectLocal, cmdPair, cmdDiscover), with the CLI flags replaced by Options.
package link

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/mdns"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
)

// Mode chooses how a frame is reached.
type Mode string

const (
	Auto  Mode = "auto"  // local network first, relay as fallback
	Local Mode = "local" // local network only
	Relay Mode = "relay" // relay only
)

// Options says who this client is and how to reach frames.
type Options struct {
	Identity *sdg.Identity
	// Name is how the frame shows this client as the sender of its photos.
	Name string
	Mode Mode
	// Discover bounds the mDNS search for a frame. Defaults to 2s.
	Discover time.Duration
	// Servers overrides Frameo's grid servers; their keys are then not
	// pinned. Only for tests against a fake grid.
	Servers []sdg.Endpoint
	Logger  *slog.Logger
}

// Via names how a frame was reached.
type Via string

const (
	ViaLocal Via = "local"
	ViaRelay Via = "relay"
)

// localDialTimeout bounds the direct connection once the frame was found: a
// frame that advertises itself but does not accept must not hold up the
// fallback to the relay.
const localDialTimeout = 5 * time.Second

func (o *Options) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (o *Options) discover() time.Duration {
	if o.Discover > 0 {
		return o.Discover
	}
	return 2 * time.Second
}

func (o *Options) dialGrid(ctx context.Context) (*sdg.Grid, error) {
	servers, keys := sdg.FrameoServers, []sdg.Key{sdg.FrameoServerKey}
	if len(o.Servers) > 0 {
		servers, keys = o.Servers, nil
	}
	return sdg.Dial(ctx, servers, o.Identity, &sdg.Options{Logger: o.logger(), ServerKeys: keys})
}

// Open connects to a paired frame. In Auto mode the relay is dialled while
// the local network is searched, so a frame that is elsewhere is no slower to
// reach than through the relay alone.
func Open(ctx context.Context, peer sdg.PeerID, o Options) (*frameo.Client, Via, error) {
	type gridDial struct {
		g   *sdg.Grid
		err error
	}
	var grid chan gridDial
	if o.Mode == Auto || o.Mode == "" {
		grid = make(chan gridDial, 1)
		go func() {
			g, err := o.dialGrid(ctx)
			grid <- gridDial{g, err}
		}()
	}
	discardGrid := func() {
		if grid != nil {
			go func() {
				if r := <-grid; r.g != nil {
					_ = r.g.Close()
				}
			}()
		}
	}

	if o.Mode != Relay {
		p, err := openLocal(ctx, peer, &o)
		switch {
		case err == nil:
			discardGrid()
			return o.client(p), ViaLocal, nil
		case o.Mode == Local:
			return nil, "", fmt.Errorf("Frame im lokalen Netz nicht erreichbar: %w", err)
		default:
			o.logger().Debug("not reachable locally, falling back to the relay", "err", err)
		}
	}

	var g *sdg.Grid
	var err error
	if grid != nil {
		r := <-grid
		g, err = r.g, r.err
	} else {
		g, err = o.dialGrid(ctx)
	}
	if err != nil {
		return nil, "", fmt.Errorf("Frameo-Server nicht erreichbar: %w", err)
	}
	p, err := g.Connect(ctx, peer, sdg.FrameoProtocol)
	if err != nil {
		_ = g.Close()
		switch {
		case errors.Is(err, sdg.ErrPeerTimeout):
			return nil, "", errors.New("Frame antwortet nicht: ausgeschaltet oder nicht im Netz")
		case errors.Is(err, sdg.ErrRefused):
			return nil, "", errors.New("Frame lehnt die Verbindung ab: Kopplung wurde am Frame entfernt")
		}
		return nil, "", err
	}
	// The peer connection stands on its own once it is up.
	go func() {
		<-p.Done()
		_ = g.Close()
	}()
	return o.client(p), ViaRelay, nil
}

func (o *Options) client(t frameo.Transport) *frameo.Client {
	return frameo.NewClient(t, &frameo.Options{Logger: o.logger(), Name: o.Name})
}

// openLocal finds the frame by mDNS and connects straight to it. Discovery is
// bounded by Options.Discover, not by ctx: a frame that is not on this network
// never answers.
func openLocal(ctx context.Context, peer sdg.PeerID, o *Options) (*sdg.Peer, error) {
	look, cancel := context.WithTimeout(ctx, o.discover())
	defer cancel()
	found, err := mdns.Lookup(look, sdg.LocalService, func(instance string) bool {
		return sdg.IsLocalInstance(instance, peer)
	}, &mdns.Options{Logger: o.logger()})
	if err != nil {
		return nil, err
	}
	addr, ok := found.Addr()
	if !ok {
		return nil, fmt.Errorf("%s meldet sich im Netz, nennt aber keine Adresse", found.Instance)
	}
	ep := sdg.Endpoint{Host: addr.String(), Port: found.Port}
	dial, cancelDial := context.WithTimeout(ctx, localDialTimeout)
	defer cancelDial()
	p, err := sdg.DialLocal(dial, ep, peer, o.Identity, sdg.LocalProtocol, &sdg.Options{Logger: o.logger()})
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%s gefunden unter %s, Verbindung aber nicht innerhalb von %v aufgebaut", found.Instance, ep, localDialTimeout)
	}
	return p, err
}

// Pair pairs with the frame showing code and returns its peer id.
func Pair(ctx context.Context, code string, o Options) (sdg.PeerID, error) {
	g, err := o.dialGrid(ctx)
	if err != nil {
		return sdg.PeerID{}, fmt.Errorf("Frameo-Server nicht erreichbar: %w", err)
	}
	defer g.Close()
	peer, err := g.Pair(ctx, code)
	switch {
	case errors.Is(err, sdg.ErrRefused):
		return peer, errors.New("Code unbekannt: Ist er noch aktuell? Codes laufen ab")
	case errors.Is(err, sdg.ErrPairingFailed):
		return peer, errors.New("Code nicht angenommen: Ziffern prüfen und erneut versuchen")
	}
	return peer, err
}

// Found is a frame advertising itself on the local network.
type Found struct {
	Instance string
	Endpoint sdg.Endpoint
}

// Discover lists the frames advertising themselves on the local network,
// paired or not.
func Discover(ctx context.Context, o Options) ([]Found, error) {
	look, cancel := context.WithTimeout(ctx, o.discover())
	defer cancel()
	services, err := mdns.Browse(look, sdg.LocalService, &mdns.Options{Logger: o.logger()})
	if err != nil {
		return nil, err
	}
	out := make([]Found, 0, len(services))
	for _, s := range services {
		host := s.Host
		if a, ok := s.Addr(); ok {
			host = a.String()
		}
		out = append(out, Found{Instance: s.Instance, Endpoint: sdg.Endpoint{Host: host, Port: s.Port}})
	}
	return out, nil
}
