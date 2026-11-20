// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/dhcgn/picture-frame-control/internal/config"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

func TestIdentityAndFramesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Identity()
	if err != nil {
		t.Fatal(err)
	}
	peer, _ := sdg.NewIdentity()
	if err := s.AddFrame(store.NewFrame(peer.Public, "Küche")); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFrame(peer.Public.String(), func(f *store.Frame) { f.Limit = 300 }); err != nil {
		t.Fatal(err)
	}
	if err := s.SetImmichAPIKey("secret-api-key"); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := again.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if id2.Private != id.Private {
		t.Error("identity changed across reopen")
	}
	if again.ImmichAPIKey() != "secret-api-key" {
		t.Error("Immich API key lost across reopen")
	}
	f, err := again.Frame(peer.Public.String())
	if err != nil || f.Name != "Küche" || f.Limit != 300 || f.Scale != "crop" {
		t.Errorf("frame after reopen = %+v, %v", f, err)
	}

	// The secrets must not sit on disk in plain text.
	raw, _ := os.ReadFile(filepath.Join(dir, "secrets.dat"))
	if bytes.Contains(raw, []byte(id.Private.String())) || bytes.Contains(raw, []byte("secret-api-key")) {
		t.Error("secrets.dat holds a secret in plain text")
	}
}

func TestImportAndExportUnframeo(t *testing.T) {
	dir := t.TempDir()
	unframeoPath := filepath.Join(dir, "unframeo", "config.json")
	uc, _, err := config.Create(unframeoPath)
	if err != nil {
		t.Fatal(err)
	}
	peer, _ := sdg.NewIdentity()
	if _, err := uc.AddFrame("wohnzimmer", peer.Public); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(filepath.Join(dir, "app"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.ImportUnframeo(unframeoPath)
	if err != nil || n != 1 {
		t.Fatalf("ImportUnframeo = %d, %v; want 1 frame", n, err)
	}
	// Imported frames follow the name the frame reports, once it answers.
	if f, _ := s.Frame(peer.Public.String()); f.Name != "" {
		t.Errorf("imported frame named %q, want empty (follow the frame)", f.Name)
	}
	_ = s.UpdateFrame(peer.Public.String(), func(f *store.Frame) { f.FrameName = "wohnzimmer" })
	id, _ := s.Identity()
	if id.Private.String() != uc.PrivateKey {
		t.Error("imported identity differs from unframeo's")
	}

	// Exporting must give back a file unframeo itself can load.
	out := filepath.Join(dir, "backup.json")
	if err := s.ExportUnframeo(out); err != nil {
		t.Fatal(err)
	}
	back, err := config.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.PrivateKey != uc.PrivateKey || back.Frames["wohnzimmer"].PeerID != peer.Public.String() {
		t.Errorf("export lost data: %+v", back)
	}

	// Another app identity with frames of its own must not be overwritten.
	other, _ := store.Open(filepath.Join(dir, "other"))
	if _, err := other.Identity(); err != nil {
		t.Fatal(err)
	}
	p2, _ := sdg.NewIdentity()
	_ = other.AddFrame(store.NewFrame(p2.Public, "Oma"))
	if _, err := other.ImportUnframeo(unframeoPath); err == nil {
		t.Error("import over an identity with paired frames must be refused")
	}
}
