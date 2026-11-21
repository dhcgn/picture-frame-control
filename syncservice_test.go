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

func TestWithYear(t *testing.T) {
	for _, tc := range []struct {
		album string
		year  int
		want  string
	}{
		{"Ostsee", 2024, "Ostsee 2024"},
		{"Hochzeit Anna 2023", 2024, "Hochzeit Anna 2023"},
		{"Urlaub2024", 2021, "Urlaub2024"},
		{"Italy 2019 2021", 2020, "Italy 2019 2021"},
		{"Retro 1900", 2024, "Retro 1900"},
		{"Zukunft 2050", 2024, "Zukunft 2050"},
		{"Jahr 1899", 2024, "Jahr 1899 2024"},
		{"Jahr 2051", 2024, "Jahr 2051 2024"},
		{"Zimmer 12345", 2024, "Zimmer 12345 2024"},
		{"Zimmer 20245", 2024, "Zimmer 20245 2024"},
		{"Italy", 2019, "Italy 2019"}, // same album, other photo, other year
		{"Italy", 2021, "Italy 2021"},
		{"", 2024, ""},
		{"Ostsee", 0, "Ostsee"},
	} {
		if got := withYear(tc.album, tc.year); got != tc.want {
			t.Errorf("withYear(%q, %d) = %q, want %q", tc.album, tc.year, got, tc.want)
		}
	}
}

func TestLocalYear(t *testing.T) {
	for _, tc := range []struct {
		local, created string
		want           int
	}{
		{"2024-12-31T23:30:00.000Z", "2025-01-01T00:30:00.000Z", 2024}, // wall clock wins
		{"nonsense", "2025-01-01T00:30:00.000Z", 2025},                 // fallback
		{"", "", 0},
	} {
		if got := localYear(immich.Asset{LocalDateTime: tc.local, FileCreatedAt: tc.created}); got != tc.want {
			t.Errorf("localYear(%q, %q) = %d, want %d", tc.local, tc.created, got, tc.want)
		}
	}
}

func TestAgeOn(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	for _, tc := range []struct {
		birth, at string
		age       int
		ok        bool
	}{
		{"1991-03-10", "2021-03-09", 29, true}, // the day before the birthday
		{"1991-03-10", "2021-03-10", 30, true}, // on the birthday
		{"1991-03-10", "2021-12-31", 30, true},
		{"2021-03-10", "2021-06-01", 0, true},  // a baby
		{"2021-03-10", "2020-06-01", 0, false}, // born after the photo
		{"", "2021-06-01", 0, false},           // no birth date
		{"garbage", "2021-06-01", 0, false},
	} {
		if age, ok := ageOn(tc.birth, day(tc.at)); age != tc.age || ok != tc.ok {
			t.Errorf("ageOn(%q, %s) = %d, %v; want %d, %v", tc.birth, tc.at, age, ok, tc.age, tc.ok)
		}
	}
	if _, ok := ageOn("1991-03-10", time.Time{}); ok {
		t.Error("unknown photo date must give no age")
	}
}

func TestPeopleCaption(t *testing.T) {
	at := time.Date(2021, 8, 1, 12, 0, 0, 0, time.UTC)
	p := func(name, birth string) immich.Person { return immich.Person{Name: name, BirthDate: birth} }
	danny, maria, martha := p("Danny", "1991-03-10"), p("Maria", "2017-01-01"), p("Martha", "1992-02-02")
	for _, tc := range []struct {
		name   string
		people []immich.Person
		want   string
	}{
		{"none", nil, ""},
		{"three", []immich.Person{martha, danny, maria}, "Danny (30), Maria (4), Martha (29)"}, // alphabetical
		{"no birth date", []immich.Person{p("Oma", ""), danny}, "Danny (30), Oma"},
		{"unnamed and hidden", []immich.Person{p("", ""), {Name: "Geheim", Hidden: true}, danny}, "Danny (30)"},
		{"four keep names", []immich.Person{danny, maria, martha, p("Zoe", "2000-01-01")}, "Danny (30), Maria (4), Martha (29), Zoe (21)"},
		{"five get initials", []immich.Person{danny, maria, martha, p("Zoe", "2000-01-01"), p("Oma", "")}, "D (30), M (4), M (29), O, Z (21)"},
		{"umlaut initial", []immich.Person{p("Östen", "1991-03-10"), danny, maria, martha, p("Zoe", "")}, "D (30), M (4), M (29), Z, Ö (30)"}, // byte order puts non-ASCII last
		{"unnamed do not count towards five", []immich.Person{danny, maria, martha, p("Zoe", ""), p("", ""), p("", "")}, "Danny (30), Maria (4), Martha (29), Zoe"},
	} {
		if got := peopleCaption(tc.people, at); got != tc.want {
			t.Errorf("%s: peopleCaption = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestJoinCaption(t *testing.T) {
	for _, tc := range [][3]string{{"Ostsee", "Danny (30)", "Ostsee + Danny (30)"}, {"Ostsee", "", "Ostsee"}, {"", "Danny (30)", "Danny (30)"}, {"", "", ""}} {
		if got := joinCaption(tc[0], tc[1]); got != tc[2] {
			t.Errorf("joinCaption(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
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
				{"id":"new","type":"IMAGE","checksum":"c3","fileCreatedAt":"2024-06-01T10:00:00.000Z","localDateTime":"2024-06-01T12:00:00.000Z","originalFileName":"new.jpg",
					 "people":[{"id":"p","name":"Maria","birthDate":"2018-06-20","isHidden":false},{"id":"q","name":"","birthDate":null,"isHidden":false}]}
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
		fr.CaptionPeople = true
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
	// Selected by tag and in two albums: captioned with the smaller one, plus
	// the named person with the age on the day (Maria turns 6 on 20 June).
	if c := photos[0].Media.GetCaption(); c != "Ostsee 2024 + Maria (5)" {
		t.Errorf("caption = %q, want the smaller album's name and the person", c)
	}
	if c := photos[1].Media.GetCaption(); c != "Ostsee 2024" {
		t.Errorf("caption without people = %q, want the album name only", c)
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
// because Frameo takes none that short. Their lengths come in both forms
// Immich has used, text and milliseconds.
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
				{"id":"blink","type":"VIDEO","checksum":"c2","fileCreatedAt":"2024-05-02T10:00:00.000Z","originalFileName":"blink.mov","duration":2000}
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
