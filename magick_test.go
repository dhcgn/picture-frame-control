// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

// fakeRenditions serves a landscape photo "land" and a portrait photo "port"
// the way Immich does: a preview 1440 pixels on the short side and a larger
// full-size image. It counts the downloads per asset and rendition.
func fakeRenditions(t *testing.T, magick string) (*SyncService, *immichCounter) {
	t.Helper()
	sizes := map[string]image.Rectangle{
		"land/preview": image.Rect(0, 0, 1920, 1440), "land/fullsize": image.Rect(0, 0, 3200, 2400),
		"port/preview": image.Rect(0, 0, 1440, 1920), "port/fullsize": image.Rect(0, 0, 2400, 3200),
		"tiny/preview": image.Rect(0, 0, 800, 600), "tiny/fullsize": image.Rect(0, 0, 800, 600), // an old scan
	}
	cnt := &immichCounter{n: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/assets/"), "/")
		key := id + "/" + r.URL.Query().Get("size")
		cnt.add(key)
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, image.NewGray(sizes[key]), nil)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetImmichAPIKey("k"); err != nil {
		t.Fatal(err)
	}
	_ = st.Update(func(c *store.Config) error { c.ImmichServer = srv.URL; c.Magick = magick; return nil })
	s := &SyncService{store: st, immich: &ImmichService{store: st}, cacheDir: t.TempDir(),
		emit: func(_ string, data any) { cnt.warn(data.(SyncLog).Text) }}
	return s, cnt
}

type immichCounter struct {
	mu       sync.Mutex
	n        map[string]int
	warnings []string
}

func (c *immichCounter) add(key string) { c.mu.Lock(); c.n[key]++; c.mu.Unlock() }
func (c *immichCounter) warn(text string) {
	c.mu.Lock()
	c.warnings = append(c.warnings, text)
	c.mu.Unlock()
}

func frameOf(w, h int, scale string, rotate bool) store.Frame {
	return store.Frame{ID: "f", Screen: store.Size{W: w, H: h}, Scale: scale, Rotate: rotate}
}

// TestMediaFileNeverBelowScreen checks, without ImageMagick, that a photo goes
// as Immich's preview only where that fills the screen, and as the full-size
// image where the frame would have to enlarge the preview.
func TestMediaFileNeverBelowScreen(t *testing.T) {
	s, cnt := fakeRenditions(t, "")
	c, err := s.immich.client()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		asset   string
		frame   store.Frame
		variant string
	}{
		{"land", frameOf(1920, 1080, "crop", false), "preview-jpg"},  // 1920×1440 covers 1920×1080
		{"port", frameOf(1920, 1080, "crop", false), "fullsize-jpg"}, // 1440 wide is short of 1920
		{"port", frameOf(1920, 1080, "fit", false), "preview-jpg"},   // fitting needs only 1080 high
		{"land", frameOf(1920, 1080, "crop", true), "fullsize-jpg"},  // portrait mount: 1440 high is short of 1920
		{"land", store.Frame{ID: "f"}, "preview-jpg"},                // screen not reported yet
		{"tiny", frameOf(1920, 1080, "crop", false), "fullsize-jpg"}, // as large as it gets, not enlarged
	} {
		_, variant, _, err := s.mediaFile(ctx, c, syncplan.Asset{ID: tc.asset, Checksum: "c"}, tc.frame)
		if err != nil || variant != tc.variant {
			t.Errorf("%s on %+v: %q, %v; want %q", tc.asset, tc.frame.Screen, variant, err, tc.variant)
		}
	}
	if cnt.n["land/preview"] != 1 || cnt.n["port/preview"] != 1 || cnt.n["port/fullsize"] != 1 || cnt.n["land/fullsize"] != 1 {
		t.Errorf("downloads %v, want each rendition once (cached)", cnt.n)
	}
}

// TestMediaFileShrinks runs mediaFile with the ImageMagick installed here:
// always from the full-size image, shrunk to cover or fit the screen, never
// enlarged, the download removed once converted, and the photo as it came
// from Immich when ImageMagick fails.
func TestMediaFileShrinks(t *testing.T) {
	magick := tools["magick"].find()
	if magick == "" {
		t.Skip("ImageMagick is not installed")
	}
	s, cnt := fakeRenditions(t, magick)
	c, err := s.immich.client()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	size := func(path string) string {
		out, err := hiddenCmd(ctx, magick, "identify", "-format", "%wx%h %m", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, tc := range []struct {
		asset    string
		frame    store.Frame
		wantSize string
		variant  string
	}{
		{"land", frameOf(1280, 800, "crop", false), "1280x960 WEBP", "webp-1280x800-cover-q82"},
		{"land", frameOf(1280, 800, "fit", false), "1067x800 WEBP", "webp-1280x800-fit-q82"},
		{"land", frameOf(1280, 800, "crop", true), "1707x1280 WEBP", "webp-800x1280-cover-q82"},
		{"port", frameOf(1920, 1080, "crop", false), "1920x2560 WEBP", "webp-1920x1080-cover-q82"},
		{"port", frameOf(1920, 1080, "fit", false), "810x1080 WEBP", "webp-1920x1080-fit-q82"},
		{"tiny", frameOf(1920, 1080, "crop", false), "800x600 WEBP", "webp-1920x1080-cover-q82"}, // never enlarged
		{"tiny", frameOf(1920, 1080, "fit", false), "800x600 WEBP", "webp-1920x1080-fit-q82"},
	} {
		path, variant, n, err := s.mediaFile(ctx, c, syncplan.Asset{ID: tc.asset, Checksum: "c"}, tc.frame)
		if err != nil {
			t.Fatal(err)
		}
		if got := size(path); got != tc.wantSize || variant != tc.variant || n == 0 {
			t.Errorf("%s on %+v %s: %s %q; want %s %q", tc.asset, tc.frame.Screen, tc.frame.Scale, got, variant, tc.wantSize, tc.variant)
		}
	}
	if n := cnt.n["land/preview"] + cnt.n["port/preview"] + cnt.n["tiny/preview"]; n != 0 || cnt.n["land/fullsize"] != 3 {
		t.Errorf("downloads %v: want only full-size images, once per conversion", cnt.n)
	}
	if left, _ := filepath.Glob(filepath.Join(s.cacheDir, "media", "*_fullsize.*")); len(left) > 0 {
		t.Errorf("full-size downloads left in the cache: %v", left)
	}

	// ImageMagick that fails: the photo still goes, as it came from Immich.
	_ = s.store.Update(func(c *store.Config) error { c.Magick = magick + "-missing.exe"; return nil })
	path, variant, _, err := s.mediaFile(ctx, c, syncplan.Asset{ID: "land", Checksum: "c", Name: "land.jpg"}, frameOf(1024, 768, "crop", false))
	if err != nil || variant != "fullsize-jpg" || !strings.HasSuffix(path, "_fullsize.jpg") {
		t.Errorf("failing ImageMagick: %s %q %v, want the full-size image", path, variant, err)
	}
	if len(cnt.warnings) != 1 || !strings.Contains(cnt.warnings[0], "ImageMagick") {
		t.Errorf("warnings = %q, want one about ImageMagick", cnt.warnings)
	}
}

func TestMagickVersion(t *testing.T) {
	magick := tools["magick"].find()
	if magick == "" {
		t.Skip("ImageMagick is not installed")
	}
	if v, err := tools["magick"].version(context.Background(), magick); err != nil || !strings.HasPrefix(v, "7.") {
		t.Errorf("version %q, %v", v, err)
	}
	// Some other program is not taken for ImageMagick.
	if _, err := tools["magick"].version(context.Background(), "cmd.exe"); err == nil {
		t.Error("cmd.exe taken for ImageMagick")
	}
}
