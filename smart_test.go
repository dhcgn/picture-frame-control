// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/frameotest"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

// TestSyncSmartFitsPortrait syncs a landscape and a portrait photo to a frame
// in smart mode: the portrait one goes with Fit, the landscape one without.
func TestSyncSmartFitsPortrait(t *testing.T) {
	jpg := func(w, h int) []byte {
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h)), nil)
		return buf.Bytes()
	}
	immichSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/search/metadata":
			_, _ = w.Write([]byte(`{"assets":{"items":[
				{"id":"land","type":"IMAGE","checksum":"c1","fileCreatedAt":"2024-06-01T10:00:00.000Z","originalFileName":"land.jpg"},
				{"id":"port","type":"IMAGE","checksum":"c2","fileCreatedAt":"2024-05-01T10:00:00.000Z","originalFileName":"port.jpg"}
			],"nextCursor":null}}`))
		case strings.HasSuffix(r.URL.Path, "/thumbnail"):
			w.Header().Set("Content-Type", "image/jpeg")
			if strings.Contains(r.URL.Path, "/land/") {
				_, _ = w.Write(jpg(1920, 1440))
			} else {
				_, _ = w.Write(jpg(1440, 1920))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer immichSrv.Close()

	frame := frameotest.New()
	frame.ListType = frameo.TypeGetAllMediaMetaData
	frames, st, f := withFakeFrame(t, frame)
	if err := st.SetImmichAPIKey("k"); err != nil {
		t.Fatal(err)
	}
	_ = st.Update(func(c *store.Config) error { c.ImmichServer = immichSrv.URL; return nil })
	_ = st.UpdateFrame(f.ID, func(fr *store.Frame) {
		fr.Sources.Tags = []string{"x"}
		fr.Scale = "smart"
		fr.Screen = store.Size{W: 1920, H: 1080}
	})
	dir := t.TempDir()
	s := &SyncService{store: st, frames: frames, immich: &ImmichService{store: st}, dataDir: dir, cacheDir: dir, emit: func(string, any) {}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if res, err := s.Run(ctx, []RunFrame{{FrameID: f.ID}}); err != nil || res[0].Sent != 2 || res[0].Error != "" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	want := map[int64]pb.Media_ScaleType{ // by capture date
		time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC).UnixMilli(): pb.Media_CENTER_POINT_CROP, // landscape
		time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC).UnixMilli(): pb.Media_FIT_INSIDE,        // portrait
	}
	photos := frame.Photos()
	if len(photos) != 2 {
		t.Fatalf("frame got %d photos, want 2", len(photos))
	}
	for _, p := range photos {
		if got := p.Media.GetScaleType(); got != want[p.Media.GetCaptureDate()] {
			t.Errorf("photo taken %d: scale type %v, want %v", p.Media.GetCaptureDate(), got, want[p.Media.GetCaptureDate()])
		}
	}
}
