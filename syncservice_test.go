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
	"github.com/dhcgn/picture-frame-control/internal/immich"
	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

func TestPickAlbum(t *testing.T) {
	albums := []immich.Album{
		{ID: "best", Name: "Best of", AssetCount: 900},
		{ID: "tiny", Name: "Oma", AssetCount: 4},
		{ID: "trip", Name: "Urlaub", AssetCount: 200},
	}
	for _, tc := range []struct {
		selected []string
		want     string
	}{
		{nil, "Oma"},                         // none selected: the smallest
		{[]string{"trip"}, "Urlaub"},         // a selected album wins over a smaller one
		{[]string{"best", "trip"}, "Urlaub"}, // among selected: the smallest
		{[]string{"elsewhere"}, "Oma"},       // selected albums it is not in do not count
	} {
		if got := pickAlbum(albums, tc.selected); got != tc.want {
			t.Errorf("pickAlbum(%v) = %q, want %q", tc.selected, got, tc.want)
		}
	}
	if got := pickAlbum(nil, nil); got != "" {
		t.Errorf("no albums: %q, want no caption", got)
	}
}

// TestSyncMirrorsImmichOntoFrame runs preview and sync end to end against a
// fake Immich and a fake frame: videos are skipped, a foreign photo goes only
// when approved, photos are sent newest first with their capture date, and a
// second preview finds them in place.
func TestSyncMirrorsImmichOntoFrame(t *testing.T) {
	immichSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/albums" && r.URL.Query().Get("assetId") != "":
			// Every photo is in two albums; neither is selected for the frame.
			_, _ = w.Write([]byte(`[{"id":"big","albumName":"Best of","assetCount":900},{"id":"small","albumName":"Ostsee 2024","assetCount":3}]`))
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
		fr.Sources.Tags = []string{"reisen/ostsee"} // selected by tag, captioned by album
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
	// Selected by tag and in two albums: captioned with the smaller one.
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

	// Force: everything this app sent is replaced, though nothing changed.
	old := []int64{m.Entries[0].MediaID, m.Entries[1].MediaID}
	res, err = s.Run(ctx, []RunFrame{{FrameID: f.ID, Force: true}})
	if err != nil || res[0].Sent != 2 || res[0].Deleted != 2 || res[0].Error != "" {
		t.Fatalf("forced Run = %+v, %v (logs %q)", res, err, logs)
	}
	if d := frame.Deleted(); !slices.Contains(d, old[0]) || !slices.Contains(d, old[1]) {
		t.Errorf("deleted %v, want the old copies %v", d, old)
	}
	if photos := frame.Photos(); len(photos) != 4 || photos[2].Media.GetCaptureDate() != want {
		t.Errorf("frame got %d photos in all, want 2 more, newest first", len(photos))
	}
	if m, _ := syncplan.Load(s.manifestPath(f.ID)); len(m.Entries) != 2 {
		t.Errorf("manifest after force = %+v, want the 2 photos once each", m.Entries)
	}
}

// TestSyncSendsVideos syncs videos with the ffmpeg installed here: a 5-second
// clip goes to the frame as a video with its still, a 2-second one is skipped
// because Frameo takes none that short.
func TestSyncSendsVideos(t *testing.T) {
	ffmpeg := tools["ffmpeg"].find()
	if ffmpeg == "" {
		t.Skip("ffmpeg is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	clip := makeClip(t, ctx, ffmpeg, "1280x720", "30", true)
	immichSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search/metadata":
			_, _ = w.Write([]byte(`{"assets":{"items":[
				{"id":"clip","type":"VIDEO","checksum":"c1","fileCreatedAt":"2024-05-01T10:00:00.000Z","originalFileName":"clip.mov","duration":"0:00:05.000000"},
				{"id":"blink","type":"VIDEO","checksum":"c2","fileCreatedAt":"2024-05-02T10:00:00.000Z","originalFileName":"blink.mov","duration":"0:00:02.000000"}
			],"nextCursor":null}}`))
		case "/api/assets/clip/original":
			http.ServeFile(w, r, clip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer immichSrv.Close()

	frame := frameotest.New()
	frame.ListType = frameo.TypeGetAllMediaMetaData
	frames, st, f := withFakeFrame(t, frame)
	_ = st.SetImmichAPIKey("k")
	_ = st.Update(func(c *store.Config) error { c.ImmichServer = immichSrv.URL; c.FFmpeg = ffmpeg; return nil })
	_ = st.UpdateFrame(f.ID, func(fr *store.Frame) {
		fr.Sources.Albums = []string{"a"}
		fr.Screen, fr.MaxVideo = store.Size{W: 1280, H: 800}, store.Size{W: 1280, H: 720}
	})
	dir := t.TempDir()
	s := &SyncService{store: st, frames: frames, immich: &ImmichService{store: st}, dataDir: dir, cacheDir: dir, emit: func(string, any) {}}

	if p := s.Preview(ctx, []string{f.ID})[0]; p.Error != "" || p.Send != 1 || p.Skipped != 1 {
		t.Fatalf("Preview = %+v, want the 5-second clip to send and the 2-second one skipped", p)
	}
	res, err := s.Run(ctx, []RunFrame{{FrameID: f.ID}})
	if err != nil || res[0].Sent != 1 || res[0].Error != "" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	got := frame.Photos()
	if len(got) != 1 || got[0].Media.GetType() != pb.Media_VIDEO_WITH_THUMB || got[0].Media.GetFileExtension() != "mp4" ||
		len(got[0].Extra) == 0 || string(got[0].Extra[:3]) != "\xff\xd8\xff" {
		t.Fatalf("frame got %d items, first %v with %d still bytes; want one video with a JPEG still", len(got), got[0].Media, len(got[0].Extra))
	}
	if m, _ := syncplan.Load(s.manifestPath(f.ID)); len(m.Entries) != 1 || m.Entries[0].Variant != "mp4-1280x720-crf23" {
		t.Errorf("manifest = %+v", m.Entries)
	}
}
