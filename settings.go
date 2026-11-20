// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dhcgn/picture-frame-control/internal/config"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

// SettingsService covers the app-wide settings (E-01..E-04, F-11, F-12).
// Immich and tool settings follow with M2 and M4.
type SettingsService struct {
	store  *store.Store
	frames *FrameService
}

// Settings is what the settings page shows.
type Settings struct {
	ClientName   string `json:"clientName"`
	HasIdentity  bool   `json:"hasIdentity"`
	PublicKey    string `json:"publicKey"`
	UnframeoPath string `json:"unframeoPath"` // unframeo's config.json, when one exists
	DataDir      string `json:"dataDir"`
}

func (s *SettingsService) Get() (Settings, error) {
	st := Settings{ClientName: s.store.Config().ClientName, HasIdentity: s.store.HasIdentity()}
	if st.HasIdentity {
		id, err := s.store.Identity()
		if err != nil {
			return st, err
		}
		st.PublicKey = id.Public.String()
	}
	if p, err := config.DefaultPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			st.UnframeoPath = p
		}
	}
	st.DataDir, _ = store.DefaultDir()
	return st, nil
}

// SetClientName changes how frames show this app as the sender of photos.
func (s *SettingsService) SetClientName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Der Absendername darf nicht leer sein")
	}
	if err := s.store.Update(func(c *store.Config) error { c.ClientName = name; return nil }); err != nil {
		return err
	}
	// Frames learn the name when a connection opens.
	s.frames.closeAll()
	return nil
}

// ImportUnframeo takes over unframeo's identity and paired frames. An empty
// path asks the user for the file. It returns how many frames were added.
func (s *SettingsService) ImportUnframeo(path string) (int, error) {
	if path == "" {
		dlg := application.Get().Dialog.OpenFile().
			SetTitle("unframeo-Konfiguration wählen").
			AddFilter("unframeo-Konfiguration", "*.json")
		if p, err := config.DefaultPath(); err == nil {
			dlg.SetDirectory(filepath.Dir(p))
		}
		var err error
		if path, err = dlg.PromptForSingleSelection(); err != nil || path == "" {
			return 0, err
		}
	}
	s.frames.closeAll()
	return s.store.ImportUnframeo(path)
}

// ExportIdentity saves the identity and frames as an unframeo configuration.
// It returns the file written, or "" when the user cancelled.
func (s *SettingsService) ExportIdentity() (string, error) {
	path, err := application.Get().Dialog.SaveFile().
		SetMessage("Sicherung der Identität speichern. Die Datei enthält den privaten Schlüssel.").
		SetFilename("picture-frame-control-identitaet.json").
		AddFilter("JSON", "*.json").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	return path, s.store.ExportUnframeo(path)
}
