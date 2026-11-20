// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/dhcgn/picture-frame-control/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[SendProgress]("send:progress")
	application.RegisterEvent[SyncProgress]("sync:progress")
	application.RegisterEvent[SyncLog]("sync:log")
}

func main() {
	// After an update, the updater re-runs this program as a helper that
	// swaps in the new binary. That has to happen before anything else starts.
	updater.HandleHelperMode()

	dir, err := store.DefaultDir()
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Fatal(err)
	}
	cacheDir := filepath.Join(cache, "picture-frame-control")
	frames := NewFrameService(st, cacheDir)
	im := &ImmichService{store: st}
	syncer := &SyncService{
		store: st, frames: frames, immich: im, dataDir: dir, cacheDir: cacheDir,
		emit: func(name string, data any) { application.Get().Event.Emit(name, data) },
	}
	updates := &UpdateService{}

	app := application.New(application.Options{
		Name:        "Picture-Frame-Control",
		Description: "Immich-Alben und -Tags auf Frameo-Bilderrahmen spiegeln",
		Services: []application.Service{
			application.NewService(frames),
			application.NewService(&SettingsService{store: st, frames: frames}),
			application.NewService(im),
			application.NewService(syncer),
			application.NewService(updates),
		},
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: frames.thumbMiddleware,
		},
	})

	updates.app = app
	if version != "dev" {
		if err := initUpdater(app); err != nil {
			log.Printf("%v (updates disabled)", err)
		} else {
			updates.enabled = true
		}
	}

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Picture-Frame-Control " + version,
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(221, 226, 230),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
