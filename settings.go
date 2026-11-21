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

// SettingsService covers the app-wide settings, the identity and
// the language.
type SettingsService struct {
	store  *store.Store
	frames *FrameService
}

// Settings is what the settings page shows.
type Settings struct {
	Language       string     `json:"language"`       // in use: "de" or "en"
	LanguageChoice string     `json:"languageChoice"` // stored: "de", "en", or "" to follow Windows
	ClientName     string     `json:"clientName"`
	HasIdentity    bool       `json:"hasIdentity"`
	PublicKey      string     `json:"publicKey"`
	UnframeoPath   string     `json:"unframeoPath"` // unframeo's config.json, when one exists
	DataDir        string     `json:"dataDir"`
	Tools          []ToolInfo `json:"tools"` // ImageMagick, ffmpeg
}

// ToolInfo is how an external program is set up.
type ToolInfo struct {
	ID      string `json:"id"`      // "magick" or "ffmpeg"
	Path    string `json:"path"`    // "" when not set up
	Version string `json:"version"` // "" when it does not start
	Error   string `json:"error"`
}

func (s *SettingsService) Get() (Settings, error) {
	cfg := s.store.Config()
	st := Settings{
		Language: i18n.Language(), LanguageChoice: cfg.Language,
		ClientName: cfg.ClientName, HasIdentity: s.store.HasIdentity(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range toolIDs {
		ti := ToolInfo{ID: id, Path: *tools[id].path(&cfg)}
		if ti.Path != "" {
			var err error
			if ti.Version, err = tools[id].version(ctx, ti.Path); err != nil {
				ti.Error = err.Error()
			}
		}
		st.Tools = append(st.Tools, ti)
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

// FindTool looks for ImageMagick ("magick") or ffmpeg ("ffmpeg") and, when
// found, uses it from now on.
func (s *SettingsService) FindTool(id string) error {
	t, ok := tools[id]
	if !ok {
		return fmt.Errorf("unknown tool %q", id)
	}
	p := t.find()
	if p == "" {
		return fmt.Errorf(i18n.T("%s nicht gefunden. Installiere es oder wähle %s.exe selbst aus", "%s not found. Install it or choose %s.exe yourself"), t.label, t.exe)
	}
	return s.useTool(id, p)
}

// ChooseTool lets the user pick the tool's program. Cancelling changes
// nothing.
func (s *SettingsService) ChooseTool(id string) error {
	t, ok := tools[id]
	if !ok {
		return fmt.Errorf("unknown tool %q", id)
	}
	p, err := application.Get().Dialog.OpenFile().
		SetTitle(fmt.Sprintf(i18n.T("%s.exe wählen", "Choose %s.exe"), t.exe)).
		AddFilter(fmt.Sprintf("%s (%s.exe)", t.label, t.exe), "*.exe").
		PromptForSingleSelection()
	if err != nil || p == "" {
		return err
	}
	return s.useTool(id, p)
}

// ClearTool stops using the tool: photos go as Immich has them, videos are
// skipped.
func (s *SettingsService) ClearTool(id string) error {
	return s.useTool(id, "")
}

func (s *SettingsService) useTool(id, p string) error {
	t, ok := tools[id]
	if !ok {
		return fmt.Errorf("unknown tool %q", id)
	}
	if p != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := t.version(ctx, p); err != nil {
			return err
		}
	}
	return s.store.Update(func(c *store.Config) error { *t.path(c) = p; return nil })
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
