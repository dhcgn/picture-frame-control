// SPDX-License-Identifier: GPL-3.0-or-later

package syncplan

import (
	"path/filepath"
	"slices"
	"testing"
)

func ids[T any](xs []T, id func(T) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = id(x)
	}
	slices.Sort(out)
	return out
}

func TestCompute(t *testing.T) {
	wanted := []Asset{
		{ID: "new", Checksum: "n"},
		{ID: "same", Checksum: "s"},
		{ID: "changed", Checksum: "c2"},
		{ID: "removedOnFrame", Checksum: "r"},
	}
	manifest := []Entry{
		{AssetID: "same", Checksum: "s", MediaID: 1},
		{AssetID: "changed", Checksum: "c1", MediaID: 2},
		{AssetID: "removedOnFrame", Checksum: "r", MediaID: 3}, // deleted by hand on the frame
		{AssetID: "unwanted", Checksum: "u", MediaID: 4},       // left the album
		{AssetID: "neverSeen", Checksum: "x", MediaID: 0},      // id never learned
	}
	onFrame := map[int64]bool{1: true, 2: true, 4: true, 99: true}

	p := Compute(wanted, manifest, onFrame)

	entry := func(e Entry) string { return e.AssetID }
	if got := ids(p.Keep, entry); !slices.Equal(got, []string{"same"}) {
		t.Errorf("Keep = %v", got)
	}
	if got := ids(p.Delete, entry); !slices.Equal(got, []string{"changed", "unwanted"}) {
		t.Errorf("Delete = %v", got)
	}
	if got := ids(p.Stale, entry); !slices.Equal(got, []string{"neverSeen", "removedOnFrame"}) {
		t.Errorf("Stale = %v", got)
	}
	// Send keeps the wanted order (newest first).
	if got := []string{p.Send[0].ID, p.Send[1].ID, p.Send[2].ID}; len(p.Send) != 3 || !slices.Equal(got, []string{"new", "changed", "removedOnFrame"}) {
		t.Errorf("Send = %v", p.Send)
	}
	if !slices.Equal(p.Foreign, []int64{99}) {
		t.Errorf("Foreign = %v", p.Foreign)
	}
}

// Turning captions on resends what is already there; unchanged captions keep it.
func TestComputeCaptionChangeResends(t *testing.T) {
	m := []Entry{{AssetID: "a", Checksum: "c", MediaID: 1}, {AssetID: "b", Checksum: "c", MediaID: 2, Caption: "Urlaub"}}
	on := map[int64]bool{1: true, 2: true}
	p := Compute([]Asset{{ID: "a", Checksum: "c", Caption: "Urlaub"}, {ID: "b", Checksum: "c", Caption: "Urlaub"}}, m, on)
	if len(p.Delete) != 1 || p.Delete[0].AssetID != "a" || len(p.Send) != 1 || p.Send[0].ID != "a" || len(p.Keep) != 1 {
		t.Errorf("plan = %+v", p)
	}
}

// Nothing wanted means everything this app sent goes, and nothing else.
func TestComputeEmptySelectionDeletesOnlyOurs(t *testing.T) {
	p := Compute(nil, []Entry{{AssetID: "a", MediaID: 1}}, map[int64]bool{1: true, 2: true})
	if len(p.Delete) != 1 || len(p.Send) != 0 || !slices.Equal(p.Foreign, []int64{2}) {
		t.Errorf("plan = %+v", p)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m", "frame.json")
	m, err := Load(path)
	if err != nil || len(m.Entries) != 0 {
		t.Fatalf("Load missing = %v, %v", m, err)
	}
	m.Entries = []Entry{{AssetID: "a", MediaID: 1}, {AssetID: "b", MediaID: 2}, {AssetID: "c", MediaID: 3}}
	m.Remove(2)
	m.RemoveEntries([]Entry{{AssetID: "c", MediaID: 3}})
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || len(back.Entries) != 1 || back.Entries[0].AssetID != "a" {
		t.Errorf("reloaded = %+v, %v", back, err)
	}
}
