// SPDX-License-Identifier: GPL-3.0-or-later

// Package syncplan decides what a mirror sync does on one frame, and keeps
// the manifest that lets it decide: which Immich asset became which photo on
// the frame.
package syncplan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Asset is an Immich asset the frame should show.
type Asset struct {
	ID       string
	Checksum string
	Taken    time.Time
	Name     string // original file name, for messages
}

// Entry records one asset this app put on the frame.
type Entry struct {
	AssetID  string    `json:"assetId"`
	Checksum string    `json:"checksum"`
	Variant  string    `json:"variant"` // how it was prepared, e.g. "preview-jpg"
	MediaID  int64     `json:"mediaId"` // the frame's id for it; 0 until known
	Taken    int64     `json:"taken"`   // capture date sent, unix ms
	Bytes    int       `json:"bytes"`
	SentAt   time.Time `json:"sentAt"`
}

// Plan is what a sync will do on one frame.
type Plan struct {
	Keep    []Entry // wanted and on the frame, unchanged
	Send    []Asset // wanted but missing or changed, newest first
	Delete  []Entry // on the frame, no longer wanted or changed in Immich
	Stale   []Entry // in the manifest but gone from the frame
	Foreign []int64 // on the frame but not sent by this app
}

// Compute compares what should be on the frame (wanted, newest first and
// already cut to the frame's limit) with what the manifest says this app put
// there and what the frame actually holds.
//
// A photo removed by hand on the frame is sent again: this is a mirror. To
// keep a photo off the frame, take it out of the album or tag in Immich.
func Compute(wanted []Asset, manifest []Entry, onFrame map[int64]bool) Plan {
	var p Plan
	want := make(map[string]Asset, len(wanted))
	for _, a := range wanted {
		want[a.ID] = a
	}
	kept := map[string]bool{}
	ours := map[int64]bool{}
	for _, e := range manifest {
		if e.MediaID == 0 || !onFrame[e.MediaID] {
			p.Stale = append(p.Stale, e)
			continue
		}
		ours[e.MediaID] = true
		if a, ok := want[e.AssetID]; ok && a.Checksum == e.Checksum && !kept[e.AssetID] {
			kept[e.AssetID] = true
			p.Keep = append(p.Keep, e)
			continue
		}
		p.Delete = append(p.Delete, e)
	}
	for _, a := range wanted {
		if !kept[a.ID] {
			p.Send = append(p.Send, a)
		}
	}
	for id := range onFrame {
		if !ours[id] {
			p.Foreign = append(p.Foreign, id)
		}
	}
	return p
}

// Manifest is the record of what this app sent to one frame, kept on disk.
type Manifest struct {
	path    string
	Entries []Entry `json:"entries"`
}

// Load reads a frame's manifest. A missing file is an empty manifest.
func Load(path string) (*Manifest, error) {
	m := &Manifest{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Save writes the manifest through a temporary file, so a crash leaves
// either the old or the new one, never half of either.
func (m *Manifest) Save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Remove drops the entries for the given frame ids.
func (m *Manifest) Remove(mediaIDs ...int64) {
	drop := map[int64]bool{}
	for _, id := range mediaIDs {
		drop[id] = true
	}
	kept := m.Entries[:0]
	for _, e := range m.Entries {
		if !drop[e.MediaID] {
			kept = append(kept, e)
		}
	}
	m.Entries = kept
}

// RemoveEntries drops exactly these entries, matched by asset and frame id.
func (m *Manifest) RemoveEntries(es []Entry) {
	drop := map[[2]any]bool{}
	for _, e := range es {
		drop[[2]any{e.AssetID, e.MediaID}] = true
	}
	kept := m.Entries[:0]
	for _, e := range m.Entries {
		if !drop[[2]any{e.AssetID, e.MediaID}] {
			kept = append(kept, e)
		}
	}
	m.Entries = kept
}
