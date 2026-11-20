// SPDX-License-Identifier: GPL-3.0-or-later

// Package store keeps the app's settings and secrets on disk.
//
// config.json holds everything that may be read in plain text. secrets.dat
// holds the client's private key (its identity: every frame is paired to it)
// and later the Immich API key, encrypted with DPAPI for the current Windows
// user. Both are written through a temporary file so a crash cannot leave
// half a file behind.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/config"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
)

// Size is a width and height in pixels.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Sources are the Immich albums and tags mirrored onto a frame.
type Sources struct {
	Albums []string `json:"albums"`
	Tags   []string `json:"tags"`
}

// Frame is one paired frame and how it is to be filled.
type Frame struct {
	ID        string    `json:"id"`        // the frame's peer id, hex
	Name      string    `json:"name"`      // the user's name for it; empty follows FrameName
	FrameName string    `json:"frameName"` // the name the frame reports for itself
	Place     string    `json:"place"`     // the location the frame reports
	PairedAt  time.Time `json:"pairedAt"`
	Caption   string    `json:"caption"` // "" for none, "album" for the Immich album name
	Screen    Size      `json:"screen"`
	MaxVideo  Size      `json:"maxVideo"`
	Rotate    bool      `json:"rotate"` // mounted portrait: swap width and height
	Scale     string    `json:"scale"`  // "crop" or "fit"
	Limit     int       `json:"limit"`  // most photos to send; 0 means no limit
	Mode      string    `json:"mode"`   // "auto", "local" or "relay"
	Sources   Sources   `json:"sources"`
}

// Config is the plain-text part of the store.
type Config struct {
	ClientName   string  `json:"clientName"`
	ImmichServer string  `json:"immichServer"` // origin, e.g. https://photos.example.lan
	Frames       []Frame `json:"frames"`
}

type secrets struct {
	PrivateKey   string `json:"privateKey,omitempty"`
	ImmichAPIKey string `json:"immichApiKey,omitempty"`
}

// Store is the on-disk state. It is safe for concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
	cfg Config
	sec secrets
}

// DefaultDir is %APPDATA%\picture-frame-control.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "picture-frame-control"), nil
}

// Open loads the store from dir. Missing files are an empty store, not an
// error: the app starts with nothing paired.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	if b, err := os.ReadFile(s.path("config.json")); err == nil {
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, fmt.Errorf("Einstellungen in %s nicht lesbar: %w", s.path("config.json"), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if b, err := os.ReadFile(s.path("secrets.dat")); err == nil {
		plain, err := unprotect(b)
		if err != nil {
			return nil, fmt.Errorf("Schlüsseldatei nicht entschlüsselbar (anderer Windows-Benutzer?): %w", err)
		}
		if err := json.Unmarshal(plain, &s.sec); err != nil {
			return nil, fmt.Errorf("Schlüsseldatei beschädigt: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.cfg.ClientName == "" {
		// Windows names the user DOMAIN\user; the frame only needs user@host.
		name := config.DefaultClientName()
		s.cfg.ClientName = name[strings.LastIndex(name, `\`)+1:]
	}
	return s, nil
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// Config returns a copy of the settings.
func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Frames = slices.Clone(s.cfg.Frames)
	return c
}

// Frame returns the frame with id.
func (s *Store) Frame(id string) (Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return Frame{}, fmt.Errorf("kein Frame mit der ID %s", id)
	}
	return s.cfg.Frames[i], nil
}

func (s *Store) index(id string) int {
	return slices.IndexFunc(s.cfg.Frames, func(f Frame) bool { return f.ID == id })
}

// Update changes the settings and saves them. fn's changes are kept only if
// the save succeeds.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Frames = slices.Clone(s.cfg.Frames)
	if err := fn(&c); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := s.write("config.json", b); err != nil {
		return err
	}
	s.cfg = c
	return nil
}

// UpdateFrame changes one frame and saves.
func (s *Store) UpdateFrame(id string, fn func(*Frame)) error {
	return s.Update(func(c *Config) error {
		i := slices.IndexFunc(c.Frames, func(f Frame) bool { return f.ID == id })
		if i < 0 {
			return fmt.Errorf("kein Frame mit der ID %s", id)
		}
		fn(&c.Frames[i])
		return nil
	})
}

// HasIdentity reports whether a private key exists yet.
func (s *Store) HasIdentity() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sec.PrivateKey != ""
}

// Identity returns the client's key pair, creating and saving one the first
// time it is needed. Only pairing should need it before one exists.
func (s *Store) Identity() (*sdg.Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sec.PrivateKey == "" {
		id, err := sdg.NewIdentity()
		if err != nil {
			return nil, err
		}
		sec := s.sec
		sec.PrivateKey = id.Private.String()
		if err := s.saveSecrets(sec); err != nil {
			return nil, err
		}
		s.sec = sec
		return id, nil
	}
	k, err := sdg.ParseKey(s.sec.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("gespeicherter Schlüssel unbrauchbar: %w", err)
	}
	return sdg.IdentityFromPrivate(k), nil
}

// ImmichAPIKey returns the stored Immich API key.
func (s *Store) ImmichAPIKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sec.ImmichAPIKey
}

// SetImmichAPIKey stores the Immich API key.
func (s *Store) SetImmichAPIKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.sec
	sec.ImmichAPIKey = key
	if err := s.saveSecrets(sec); err != nil {
		return err
	}
	s.sec = sec
	return nil
}

func (s *Store) saveSecrets(sec secrets) error {
	b, err := json.Marshal(sec)
	if err != nil {
		return err
	}
	enc, err := protect(b)
	if err != nil {
		return fmt.Errorf("Verschlüsseln fehlgeschlagen: %w", err)
	}
	return s.write("secrets.dat", enc)
}

func (s *Store) write(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(name + ".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(name)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// NewFrame is a frame just paired, with the defaults every frame starts from.
// An empty name follows the name the frame reports.
func NewFrame(peer sdg.PeerID, name string) Frame {
	return Frame{ID: peer.String(), Name: name, PairedAt: time.Now().UTC(), Scale: "crop", Mode: "auto"}
}

// DisplayName is the user's name for the frame, else the place the frame
// reports, else its own name, else a name made from its id. The place comes
// before the name because Frameo's "name" is the owner's: every frame of one
// person would carry the same one.
func (f Frame) DisplayName() string {
	for _, n := range []string{f.Name, f.Place, f.FrameName} {
		if n != "" {
			return n
		}
	}
	return "Frame " + f.ID[:min(6, len(f.ID))]
}

// AddFrame records a pairing. Pairing a known frame again keeps its settings.
func (s *Store) AddFrame(f Frame) error {
	return s.Update(func(c *Config) error {
		if slices.ContainsFunc(c.Frames, func(x Frame) bool { return x.ID == f.ID }) {
			return nil
		}
		c.Frames = append(c.Frames, f)
		return nil
	})
}

// RemoveFrame forgets a frame.
func (s *Store) RemoveFrame(id string) error {
	return s.Update(func(c *Config) error {
		c.Frames = slices.DeleteFunc(c.Frames, func(f Frame) bool { return f.ID == id })
		return nil
	})
}

// ImportUnframeo takes over the identity and paired frames of an unframeo
// configuration, so frames paired with unframeo need no new pairing. It
// refuses when this app already has frames paired to a different key: those
// would stop working.
func (s *Store) ImportUnframeo(path string) (int, error) {
	uc, err := config.Load(path)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	clash := s.sec.PrivateKey != "" && s.sec.PrivateKey != uc.PrivateKey && len(s.cfg.Frames) > 0
	s.mu.Unlock()
	if clash {
		return 0, errors.New("diese App hat bereits Frames mit einem anderen Schlüssel gekoppelt; ein Import würde sie unbrauchbar machen")
	}

	s.mu.Lock()
	sec := s.sec
	sec.PrivateKey = uc.PrivateKey
	err = s.saveSecrets(sec)
	if err == nil {
		s.sec = sec
	}
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}

	n := 0
	err = s.Update(func(c *Config) error {
		if uc.ClientName != "" {
			c.ClientName = uc.ClientName
		}
		for _, name := range uc.Names() {
			uf := uc.Frames[name]
			if slices.ContainsFunc(c.Frames, func(f Frame) bool { return f.ID == uf.PeerID }) {
				continue
			}
			peer, err := sdg.ParseKey(uf.PeerID)
			if err != nil {
				return fmt.Errorf("Frame %q: %w", name, err)
			}
			// unframeo's names are command-line handles; the frame's own name
			// is taken over when it first answers.
			f := NewFrame(peer, "")
			if !uf.PairedAt.IsZero() {
				f.PairedAt = uf.PairedAt
			}
			c.Frames = append(c.Frames, f)
			n++
		}
		return nil
	})
	return n, err
}

// ExportUnframeo writes the identity and frames as an unframeo configuration:
// a backup that unframeo itself can use. The file holds the private key in
// plain text and must be kept somewhere safe.
func (s *Store) ExportUnframeo(path string) error {
	id, err := s.Identity()
	if err != nil {
		return err
	}
	cfg := s.Config()
	uc := config.Unsaved(path)
	uc.PrivateKey = id.Private.String()
	uc.ClientName = cfg.ClientName
	for _, f := range cfg.Frames {
		name := f.DisplayName()
		if _, taken := uc.Frames[name]; taken {
			name += "-" + f.ID[:6] // unframeo names frames by a unique handle
		}
		uc.Frames[name] = config.Frame{PeerID: f.ID, Name: name, PairedAt: f.PairedAt}
		if uc.DefaultFrame == "" {
			uc.DefaultFrame = name
		}
	}
	return uc.Save()
}
