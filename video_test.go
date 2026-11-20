// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

// makeClip makes a 5-second test video with ffmpeg, as a camera might store it.
func makeClip(t *testing.T, ctx context.Context, ffmpeg, size, rate string, sound bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mov")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=" + size + ":rate=" + rate + ":duration=5"}
	if sound {
		args = append(args, "-f", "lavfi", "-i", "sine=duration=5")
	}
	if out, err := hiddenCmd(ctx, ffmpeg, append(args, "-c:v", "libx264", "-preset", "ultrafast", path)...).CombinedOutput(); err != nil {
		t.Fatalf("making a %s clip: %v %s", size, err, out)
	}
	return path
}

func TestVideoBoxAndRate(t *testing.T) {
	full := store.Size{W: 1920, H: 1088} // what a 15" frame reports it can play
	for _, tc := range []struct {
		frame store.Frame
		w, h  int
	}{
		{store.Frame{Screen: store.Size{W: 1920, H: 1080}, MaxVideo: full}, 1920, 1080},
		{store.Frame{Screen: store.Size{W: 1920, H: 1080}, MaxVideo: full, Rotate: true}, 1080, 1920},
		{store.Frame{Screen: store.Size{W: 1280, H: 800}, MaxVideo: store.Size{W: 1280, H: 720}}, 1280, 720},
		{store.Frame{}, 1280, 720}, // nothing reported yet
	} {
		if w, h := videoBox(tc.frame); w != tc.w || h != tc.h {
			t.Errorf("videoBox(%+v) = %dx%d, want %dx%d", tc.frame, w, h, tc.w, tc.h)
		}
	}
	for _, tc := range []struct {
		w, h   int
		length time.Duration
		want   int
	}{
		{1920, 1080, 30 * time.Second, 10_000_000}, // 15": up to 10 Mbps
		{1280, 720, 30 * time.Second, 5_000_000},   // 7" and 10": up to 5 Mbps
		{1920, 1080, videoMax, 3_081_605},          // 4:59 must stay under 125 MB
		{1920, 1080, 0, 3_081_605},                 // unknown length: as if 4:59
		{1920, 1080, time.Hour, 3_081_605},         // cut at 4:59
	} {
		if got := videoRate(tc.w, tc.h, tc.length); got < tc.want-1000 || got > tc.want+1000 {
			t.Errorf("videoRate(%d, %d, %s) = %d, want about %d", tc.w, tc.h, tc.length, got, tc.want)
		}
	}
}

// TestVideoFile converts videos from a fake Immich with the ffmpeg installed
// here: fitted inside the frame's screen and never enlarged, 30 fps, AAC
// sound, a JPEG still, and cached.
func TestVideoFile(t *testing.T) {
	ffmpeg := tools["ffmpeg"].find()
	if ffmpeg == "" {
		t.Skip("ffmpeg is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	clip := func(size, rate string, sound bool) string { return makeClip(t, ctx, ffmpeg, size, rate, sound) }
	originals := map[string]string{
		"wide":  clip("2560x1440", "60", true), // a 1440p60 clip with sound
		"tall":  clip("720x1280", "30", false), // a phone clip, no sound
		"small": clip("640x360", "25", true),   // an old clip
	}
	var fetched atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/assets/"), "/")
		fetched.Add(1)
		http.ServeFile(w, r, originals[id])
	}))
	defer srv.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetImmichAPIKey("k")
	_ = st.Update(func(c *store.Config) error { c.ImmichServer = srv.URL; c.FFmpeg = ffmpeg; return nil })
	s := &SyncService{store: st, immich: &ImmichService{store: st}, cacheDir: t.TempDir(), emit: func(string, any) {}}
	c, err := s.immich.client()
	if err != nil {
		t.Fatal(err)
	}
	frame := store.Frame{ID: "f", Screen: store.Size{W: 1920, H: 1080}, MaxVideo: store.Size{W: 1920, H: 1088}}

	probe := func(path string) string {
		out, _ := hiddenCmd(ctx, ffmpeg, "-hide_banner", "-i", path).CombinedOutput() // no output file: describes the input and fails
		return string(out)
	}
	dims := regexp.MustCompile(`Video: h264.*?, (\d+x\d+)`)
	for _, tc := range []struct {
		id, wantSize string
		sound        bool
	}{
		{"wide", "1920x1080", true},
		{"tall", "608x1080", false}, // fits the landscape screen
		{"small", "640x360", true},  // never enlarged
	} {
		video, thumb, variant, n, err := s.videoFile(ctx, c, syncplan.Asset{ID: tc.id, Checksum: "c", Video: true, Length: 5 * time.Second}, frame)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		info := probe(video)
		m := dims.FindStringSubmatch(info)
		if m == nil || m[1] != tc.wantSize || !regexp.MustCompile(`, (25|30) fps`).MatchString(info) || strings.Contains(info, "Audio: aac") != tc.sound {
			t.Errorf("%s: %q as %q, want %s at no more than 30 fps, sound %t:\n%s", tc.id, variant, m, tc.wantSize, tc.sound, info)
		}
		if variant != "mp4-1920x1080-crf23" || n == 0 {
			t.Errorf("%s: variant %q, %d bytes", tc.id, variant, n)
		}
		if b, err := os.ReadFile(thumb); err != nil || len(b) < 3 || string(b[:3]) != "\xff\xd8\xff" {
			t.Errorf("%s: still %s is not a JPEG (%v)", tc.id, thumb, err)
		}
	}
	if _, _, _, _, err := s.videoFile(ctx, c, syncplan.Asset{ID: "wide", Checksum: "c", Video: true}, frame); err != nil || fetched.Load() != 3 {
		t.Errorf("second time: %v, %d downloads; want it from the cache", err, fetched.Load())
	}
	if left, _ := filepath.Glob(filepath.Join(s.cacheDir, "media", "original-*")); len(left) > 0 {
		t.Errorf("downloaded originals left behind: %v", left)
	}
}
