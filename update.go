// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	githubprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"

	"github.com/dhcgn/picture-frame-control/internal/i18n"
)

// appRepo is where releases are published; the updater looks there.
const appRepo = "dhcgn/picture-frame-control"

// version is set at release time with -ldflags "-X main.version=v1.2.3".
// Local and CI builds report "dev" and never look for updates.
var version = "dev"

// initUpdater points the Wails self-updater at this project's GitHub
// releases. Each release ships picture-frame-control_<version>_windows_amd64.exe,
// which the default asset matcher finds by "windows" and "amd64", and
// SHA256SUMS, against which every download is verified. Pre-releases stay out
// of the feed. No CheckInterval: nothing is checked or downloaded unless the
// app asks.
//
// ponytail: checksums only; Ed25519 signatures (updater.Config.PublicKey) need a signing step in release.yml first
func initUpdater(app *application.App) error {
	gh, err := githubprovider.New(githubprovider.Config{
		Repository:    appRepo,
		ChecksumAsset: "SHA256SUMS",
	})
	if err != nil {
		return fmt.Errorf("updater: github provider: %w", err)
	}
	return app.Updater.Init(updater.Config{
		CurrentVersion: strings.TrimPrefix(version, "v"),
		Providers:      []updater.Provider{gh},
	})
}

// UpdateService tells the UI which version runs and whether a newer release
// exists, and installs one when the user asks.
type UpdateService struct {
	app     *application.App
	enabled bool // false in dev builds, or when the updater failed to start
}

// UpdateInfo is the update state shown in the app.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
}

func (s *UpdateService) info() UpdateInfo {
	return UpdateInfo{Current: version, URL: "https://github.com/" + appRepo + "/releases/latest", Enabled: s.enabled}
}

// Version reports the running version without going online.
func (s *UpdateService) Version() UpdateInfo {
	return s.info()
}

// Check asks GitHub for a newer release. Offline, rate-limited or dev builds
// simply report nothing available: a missing network is not an error here.
func (s *UpdateService) Check(ctx context.Context) UpdateInfo {
	u := s.info()
	if !s.enabled {
		return u
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rel, err := s.app.Updater.Check(ctx)
	if err != nil || rel == nil {
		return u
	}
	u.Latest = "v" + strings.TrimPrefix(rel.Version, "v")
	if url, ok := rel.Metadata["github.release.htmlURL"].(string); ok && url != "" {
		u.URL = url
	}
	u.Available = true
	return u
}

// Install opens the Wails update window, which downloads the new release,
// verifies its checksum, replaces this program and restarts it. It only runs
// when the user clicks; nothing is ever downloaded on its own.
func (s *UpdateService) Install(ctx context.Context) error {
	if !s.enabled {
		return errors.New(i18n.T("Diese Version aktualisiert sich nicht selbst (Entwicklungsversion)",
			"This version does not update itself (development build)"))
	}
	return s.app.Updater.CheckAndInstall(ctx)
}
