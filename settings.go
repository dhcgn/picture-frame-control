// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dhcgn/picture-frame-control/internal/config"
	"github.com/dhcgn/picture-frame-control/internal/i18n"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

// SettingsService covers the app-wide settings (E-01..E-04, F-11, F-12) and
// the language.
type SettingsService struct {
	store  *store.Store
	frames *FrameService
}

// Settings is what the settings page shows.
type Settings struct {
	Language       string `json:"language"`       // in use: "de" or "en"
	LanguageChoice string `json:"languageChoice"` // stored: "de", "en", or "" to follow Windows
	ClientName     string `json:"clientName"`
	HasIdentity    bool   `json:"hasIdentity"`
	PublicKey      string `json:"publicKey"`
	UnframeoPath   string `json:"unframeoPath"` // unframeo's config.json, when one exists
	DataDir        string `json:"dataDir"`
	Magick         string `json:"magick"`        // magick.exe, "" when not set up
	MagickVersion  string `json:"magickVersion"` // "" when it does not start
	MagickError    string `json:"magickError"`
}

func (s *SettingsService) Get() (Settings, error) {
	cfg := s.store.Config()
	st := Settings{
		Language: i18n.Language(), LanguageChoice: cfg.Language,
		ClientName: cfg.ClientName, HasIdentity: s.store.HasIdentity(),
		Magick: cfg.Magick,
	}
	if cfg.Magick != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		if st.MagickVersion, err = magickVersion(ctx, cfg.Magick); err != nil {
			st.MagickError = err.Error()
		}
	}
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

// SetLanguage stores the language choice ("de", "en", or "" to follow
// Windows) and switches to it. It returns the language now in use.
func (s *SettingsService) SetLanguage(choice string) (string, error) {
	if choice != "" && choice != "de" && choice != "en" {
		return "", fmt.Errorf("unknown language %q", choice)
	}
	if err := s.store.Update(func(c *store.Config) error { c.Language = choice; return nil }); err != nil {
		return "", err
	}
	lang := i18n.Resolve(choice)
	i18n.SetLanguage(lang)
	return lang, nil
}

// FindMagick looks for ImageMagick and, when found, shrinks photos with it
// from now on (M-02, M-05).
func (s *SettingsService) FindMagick() error {
	p := findMagick()
	if p == "" {
		return errors.New(i18n.T("ImageMagick nicht gefunden. Installiere es von imagemagick.org oder wähle magick.exe selbst aus",
			"ImageMagick not found. Install it from imagemagick.org or choose magick.exe yourself"))
	}
	return s.useMagick(p)
}

// ChooseMagick lets the user pick magick.exe. Cancelling changes nothing.
func (s *SettingsService) ChooseMagick() error {
	p, err := application.Get().Dialog.OpenFile().
		SetTitle(i18n.T("magick.exe wählen", "Choose magick.exe")).
		AddFilter("ImageMagick (magick.exe)", "*.exe").
		PromptForSingleSelection()
	if err != nil || p == "" {
		return err
	}
	return s.useMagick(p)
}

// ClearMagick goes back to sending Immich's preview as it is.
func (s *SettingsService) ClearMagick() error {
	return s.store.Update(func(c *store.Config) error { c.Magick = ""; return nil })
}

func (s *SettingsService) useMagick(p string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := magickVersion(ctx, p); err != nil {
		return err
	}
	return s.store.Update(func(c *store.Config) error { c.Magick = p; return nil })
}

// SetClientName changes how frames show this app as the sender of photos.
func (s *SettingsService) SetClientName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(i18n.T("Der Absendername darf nicht leer sein", "The sender name must not be empty"))
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
			SetTitle(i18n.T("unframeo-Konfiguration wählen", "Choose the unframeo configuration")).
			AddFilter(i18n.T("unframeo-Konfiguration", "unframeo configuration"), "*.json")
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
		SetMessage(i18n.T("Sicherung der Identität speichern. Die Datei enthält den privaten Schlüssel.",
			"Save a backup of the identity. The file contains the private key.")).
		SetFilename(i18n.T("picture-frame-control-identitaet.json", "picture-frame-control-identity.json")).
		AddFilter("JSON", "*.json").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	return path, s.store.ExportUnframeo(path)
}
