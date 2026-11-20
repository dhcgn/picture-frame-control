// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/frameotest"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

// TestSyncMirrorsImmichOntoFrame runs preview and sync end to end against a
// fake Immich and a fake frame: videos are skipped, a foreign photo goes only
// when approved, photos are sent newest first with their capture date, and a
// second preview finds them in place.
func TestSyncMirrorsImmichOntoFrame(t *testing.T) {
	immichSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte(`[{"id":"big","albumName":"Best of","assetCount":900},{"id":"album","albumName":"Ostsee 2024","assetCount":3}]`))
		case r.URL.Path == "/api/search/metadata":
			_, _ = w.Write([]byte(`{"assets":{"items":[
				{"id":"old","type":"IMAGE","checksum":"c1","fileCreatedAt":"2024-05-01T10:00:00.000Z","originalFileName":"old.jpg"},
				{"id":"vid","type":"VIDEO","checksum":"c2","fileCreatedAt":"2024-07-01T10:00:00.000Z"},
				{"id":"new","type":"IMAGE","checksum":"c3","fileCreatedAt":"2024-06-01T10:00:00.000Z","originalFileName":"new.jpg"}
			],"nextCursor":null}}`))
		case strings.HasSuffix(r.URL.Path, "/thumbnail"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("\xff\xd8\xffpreview " + r.URL.Path))
		default:
			http.NotFound(w, r)
		}
	}))
	defer immichSrv.Close()

	frame := frameotest.New()
	frame.ListType = frameo.TypeGetAllMediaMetaData
	frame.DeleteType = frameo.TypeDeleteMedia
	frame.Library = []*pb.MediaMetaData{{MediaId: 500, IsVisible: true}} // sent from a phone
	frames, st, f := withFakeFrame(t, frame)
	if err := st.SetImmichAPIKey("k"); err != nil {
		t.Fatal(err)
	}
	_ = st.Update(func(c *store.Config) error { c.ImmichServer = immichSrv.URL; return nil })
	_ = st.UpdateFrame(f.ID, func(fr *store.Frame) {
		fr.Sources.Albums = []string{"big", "album"} // the fake puts every photo in both
		fr.Caption = "album"
	})

	var mu sync.Mutex
	var logs []string
	dir := t.TempDir()
	s := &SyncService{
		store: st, frames: frames, immich: &ImmichService{store: st}, dataDir: dir, cacheDir: dir,
		emit: func(name string, data any) {
			if l, ok := data.(SyncLog); ok {
				mu.Lock()
				logs = append(logs, l.Text)
				mu.Unlock()
			}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plans := s.Preview(ctx, []string{f.ID})
	p := plans[0]
	if p.Error != "" || p.Send != 2 || p.Skipped != 1 || !slices.Equal(p.Foreign, []string{"500"}) || !p.CanDelete {
		t.Fatalf("Preview = %+v", p)
	}

	res, err := s.Run(ctx, []RunFrame{{FrameID: f.ID, DeleteForeign: []string{"500", "12345"}}})
	if err != nil || len(res) != 1 || res[0].Sent != 2 || res[0].Deleted != 1 || res[0].Error != "" {
		t.Fatalf("Run = %+v, %v (logs %q)", res, err, logs)
	}
	if !slices.Equal(frame.Deleted(), []int64{500}) {
		t.Errorf("deleted %v, want only the approved foreign photo 500", frame.Deleted())
	}
	photos := frame.Photos()
	want := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	if len(photos) != 2 || photos[0].Media.GetCaptureDate() != want {
		t.Fatalf("frame got %d photos, first taken %d; want 2, newest (%d) first", len(photos), photos[0].Media.GetCaptureDate(), want)
	}
	// In both albums, the photo is captioned with the smaller one.
	if c := photos[0].Media.GetCaption(); c != "Ostsee 2024" {
		t.Errorf("caption = %q, want the smaller album's name", c)
	}

	m, _ := syncplan.Load(s.manifestPath(f.ID))
	if len(m.Entries) != 2 || m.Entries[0].AssetID != "new" || m.Entries[0].MediaID == 0 {
		t.Fatalf("manifest = %+v", m.Entries)
	}

	// The frame now lists the photos under the ids the client gave them.
	frame.Library = []*pb.MediaMetaData{
		{MediaId: m.Entries[0].MediaID, CaptureDate: m.Entries[0].Taken},
		{MediaId: m.Entries[1].MediaID, CaptureDate: m.Entries[1].Taken},
	}
	again := s.Preview(ctx, []string{f.ID})[0]
	if again.Keep != 2 || again.Send != 0 || again.Delete != 0 || len(again.Foreign) != 0 {
		t.Errorf("second Preview = %+v, want everything kept", again)
	}
}
