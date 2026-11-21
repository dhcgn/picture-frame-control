// SPDX-License-Identifier: GPL-3.0-or-later

//go:build screenshots

// TestScreenshots takes the screenshots in docs/screenshots. It builds the
// app in server mode, runs it against a fake Immich and two fake frames
// filled with made-up albums and drawn landscapes, and walks a headless
// Chrome or Edge through every view. The app gets a profile of its own, so
// nothing of this machine's settings is read or changed:
//
//	wails3 task screenshots
//
// or, with the frontend already built:
//
//	go test -tags screenshots -run TestScreenshots -v .
//
// The app runs as a program of its own because Wails finds the services by
// their package path, which is "main" only in a real build.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/frameotest"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
	"github.com/dhcgn/picture-frame-control/internal/immich"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
	"github.com/dhcgn/picture-frame-control/internal/sdg/sdgtest"
	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

func TestScreenshots(t *testing.T) {
	out, err := filepath.Abs(filepath.Join("docs", "screenshots"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	// A profile of its own, so the real settings stay untouched.
	home := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(home, "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "Local"))
	exe := filepath.Join(home, "picture-frame-control-demo.exe")
	if out, err := exec.Command("go", "build", "-tags", "server", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("building the app: %v\n%s", err, out)
	}

	lib := newDemoLibrary()
	immichURL := lib.serve(t)
	grid, err := sdgtest.NewGrid()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = grid.Close() })

	dataDir, err := store.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetImmichAPIKey("demo"); err != nil {
		t.Fatal(err)
	}
	// ImageMagick as it would be set up, ffmpeg not: the settings show both
	// states, and the sync preview shows videos skipped.
	_ = st.Update(func(c *store.Config) error {
		c.Language, c.ClientName, c.ImmichServer = "en", "Picture-Frame-Control", immichURL
		c.Magick = tools["magick"].find()
		return nil
	})

	lib.addFrame(t, grid, st, dataDir, demoFrame{
		place: "Living room", screen: store.Size{W: 1280, H: 800}, caption: "album", limit: 500,
		albums: []string{"summer", "garden"}, tags: []string{"tag-pets"},
		unsent: 4, stale: true, foreign: 3, hidden: 1, firstID: 5001,
	})
	lib.addFrame(t, grid, st, dataDir, demoFrame{
		place: "Kitchen", screen: store.Size{W: 1024, H: 600}, rotate: true,
		tags: []string{"tag-family", "tag-mountains"}, unsent: 3, firstID: 6001,
	})

	app := exec.Command(exe)
	app.Env = append(os.Environ(), fmt.Sprintf("PFC_DEV_GRID=%s:%d", grid.Host(), grid.Port()))
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Process.Kill(); _ = app.Wait() })
	const appURL = "http://localhost:8080/"
	waitFor(t, appURL)

	actx, cancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser(t)), chromedp.WindowSize(1280, 800))...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(actx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	// "Start sync" asks before deleting; say yes.
	dialogs := chromedp.Events(ctx, page.JavascriptDialogOpening)
	go func() {
		for _, err := range dialogs {
			if err != nil {
				return
			}
			_, _ = chromedp.Call(ctx, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{Accept: true})
		}
	}()

	do := func(what string, steps ...chromedp.Action[chromedp.Void]) {
		t.Helper()
		if err := chromedp.Do(ctx, steps...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	view := func(height int) chromedp.Action[chromedp.Void] {
		return chromedp.EmulateViewport(1280, int64(height), chromedp.EmulateScale(1.5))
	}
	// shoot saves what the browser shows, height pixels tall; 0 makes it as
	// tall as the page, so that nothing is cut off.
	shoot := func(name string, height int) {
		t.Helper()
		if height == 0 {
			h, err := chromedp.Run(ctx, chromedp.Evaluate[int](`document.documentElement.scrollHeight`))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			height = max(800, min(h, 1800))
		}
		do(name, view(height), chromedp.Sleep(800*time.Millisecond))
		png, err := chromedp.Run(ctx, chromedp.CaptureScreenshot())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(out, name+".png"), png, 0o644); err != nil {
			t.Fatal(err)
		}
		do(name, view(800))
		t.Logf("%s.png", name)
	}
	button := func(text string) chromedp.Action[chromedp.Void] {
		return chromedp.Click(fmt.Sprintf(`//button[contains(normalize-space(.), %q)]`, text))
	}
	nav := func(text string) chromedp.Action[chromedp.Void] {
		return chromedp.Click(fmt.Sprintf(`//nav//button[contains(normalize-space(.), %q)]`, text))
	}
	tab := func(text string) chromedp.Action[chromedp.Void] {
		return chromedp.Click(fmt.Sprintf(`//button[@role="tab" and contains(., %q)]`, text))
	}

	do("frames", view(800), chromedp.Navigate(appURL),
		chromedp.WaitVisible(chromedp.CSS(`button.hang`)), chromedp.Sleep(4*time.Second))
	shoot("frames", 800)
	do("pair", button("Pair a frame"), chromedp.WaitVisible(chromedp.CSS(`dialog[open]`)))
	shoot("pair", 800)
	do("close pair", chromedp.KeyEvent(kb.Escape), chromedp.WaitNotPresent(chromedp.CSS(`dialog[open]`)))
	do("frame photos", chromedp.Click(chromedp.CSS(`button[aria-label="Open Living room"]`)),
		chromedp.WaitVisible(chromedp.CSS(`.tile img`)), chromedp.Sleep(3*time.Second))
	shoot("frame-photos", 0)
	do("frame sources", tab("Sources"))
	shoot("frame-sources", 800)
	do("frame settings", tab("Settings"))
	shoot("frame-settings", 0)
	// The matrix scrolls inside the window, so the window is made tall
	// enough for all of it.
	do("assignment", nav("Assignment"), chromedp.WaitVisible(chromedp.CSS(`table tfoot .big`)), chromedp.Sleep(2*time.Second))
	shoot("assignment", 1000)
	do("sync preview", nav("Sync"), chromedp.WaitVisible(chromedp.CSS(`.plan .facts`)), chromedp.Sleep(2*time.Second))
	shoot("sync-preview", 0)
	do("sync", button("Start sync"),
		chromedp.WaitVisible(`//div[contains(@class, "note") and contains(., "Last sync")]`), chromedp.Sleep(2*time.Second))
	shoot("sync-done", 0)
	do("settings", chromedp.Click(chromedp.CSS(`button[aria-label="Close notice"]`)), nav("Settings"),
		chromedp.WaitVisible(`//h2[contains(., "Identity")]`), chromedp.Sleep(time.Second))
	shoot("settings", 0)
}

// browser finds Chrome or Edge; PFC_BROWSER names another.
func browser(t *testing.T) string {
	if p := os.Getenv("PFC_BROWSER"); p != "" {
		return p
	}
	for _, p := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("neither Chrome nor Edge found; set PFC_BROWSER")
	return ""
}

func waitFor(t *testing.T, url string) {
	t.Helper()
	for range 100 {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not come up", url)
}

// demoAsset is a made-up photo or video in the fake Immich.
type demoAsset struct {
	ID, Name     string
	Video        bool
	Portrait     bool
	Taken        time.Time
	Albums, Tags []string
	Kind         string // what the drawn landscape shows
	Seed         int
}

type demoLibrary struct {
	assets []demoAsset
	albums []immich.Album
	tags   []immich.Tag

	mu    sync.Mutex
	jpegs map[string][]byte
}

func newDemoLibrary() *demoLibrary {
	l := &demoLibrary{
		albums: []immich.Album{
			{ID: "summer", Name: "Summer 2024"}, {ID: "christmas", Name: "Christmas 2023"},
			{ID: "garden", Name: "Our garden"}, {ID: "alps", Name: "Hiking the Alps"},
			{ID: "best", Name: "Best of", Shared: true},
		},
		tags: []immich.Tag{
			{ID: "tag-family", Name: "Family", Value: "Family"},
			{ID: "tag-pets", Name: "Pets", Value: "Pets"},
			{ID: "tag-travel", Name: "travel", Value: "travel"},
			{ID: "tag-coast", Name: "coast", Value: "travel/coast", ParentID: "tag-travel"},
			{ID: "tag-mountains", Name: "mountains", Value: "travel/mountains", ParentID: "tag-travel"},
		},
		jpegs: map[string][]byte{},
	}
	groups := []struct {
		album, kind string
		n           int
		start       time.Time
		tags        []string
		videos      []int
	}{
		{"summer", "beach", 22, time.Date(2024, 7, 12, 10, 0, 0, 0, time.UTC), []string{"tag-coast"}, []int{5}},
		{"christmas", "snow", 14, time.Date(2023, 12, 23, 16, 0, 0, 0, time.UTC), []string{"tag-family"}, []int{9}},
		{"garden", "garden", 12, time.Date(2024, 5, 4, 9, 0, 0, 0, time.UTC), []string{"tag-pets"}, nil},
		{"alps", "mountain", 16, time.Date(2023, 8, 14, 8, 0, 0, 0, time.UTC), []string{"tag-mountains"}, []int{6}},
	}
	n := 2001
	for _, g := range groups {
		for i := range g.n {
			a := demoAsset{
				ID: fmt.Sprintf("%s-%02d", g.album, i), Kind: g.kind, Seed: n,
				Taken: g.start.Add(time.Duration(i) * 97 * time.Minute), Albums: []string{g.album},
				Video: slices.Contains(g.videos, i), Portrait: i%5 == 2,
			}
			a.Name = fmt.Sprintf("IMG_%04d.jpg", n)
			if a.Video {
				a.Name = fmt.Sprintf("VID_%04d.mp4", n)
			}
			if i%3 == 0 {
				a.Albums = append(a.Albums, "best")
			}
			if i%2 == 0 {
				a.Tags = append(a.Tags, g.tags...)
			}
			if i%4 == 1 && g.album != "christmas" {
				a.Tags = append(a.Tags, "tag-family")
			}
			l.assets = append(l.assets, a)
			n++
		}
	}
	for i := range l.albums {
		for _, a := range l.assets {
			if slices.Contains(a.Albums, l.albums[i].ID) {
				l.albums[i].AssetCount++
			}
		}
	}
	return l
}

// match is what Immich's search filter selects: assets in any of the albums
// or with any of the tags (child tags included), of the given types.
func (l *demoLibrary) match(f map[string]any) []demoAsset {
	strs := func(v any, key string) (out []string) {
		m, _ := v.(map[string]any)
		xs, _ := m[key].([]any)
		for _, x := range xs {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	branches := []map[string]any{f}
	if or, ok := f["or"].([]any); ok {
		branches = nil
		for _, b := range or {
			m, _ := b.(map[string]any)
			branches = append(branches, m)
		}
	}
	types := strs(f["type"], "in")
	parent := map[string]string{}
	for _, tg := range l.tags {
		parent[tg.ID] = tg.ParentID
	}
	var out []demoAsset
	for _, a := range l.assets {
		typ := immich.Image
		if a.Video {
			typ = immich.Video
		}
		if len(types) > 0 && !slices.Contains(types, typ) {
			continue
		}
		hit := false
		for _, b := range branches {
			for _, id := range strs(b["albumIds"], "any") {
				hit = hit || slices.Contains(a.Albums, id)
			}
			for _, id := range strs(b["tagIds"], "any") {
				for _, tg := range a.Tags {
					hit = hit || tg == id || parent[tg] == id
				}
			}
		}
		if hit {
			out = append(out, a)
		}
	}
	return out
}

func (l *demoLibrary) albumsOf(a demoAsset) (out []immich.Album) {
	for _, al := range l.albums {
		if slices.Contains(a.Albums, al.ID) {
			out = append(out, al)
		}
	}
	return out
}

// photo is the asset drawn at a size: "preview" as Immich makes it (1440
// pixels on the short side), "fullsize", or "frame" for a frame's own copy.
func (l *demoLibrary) photo(a demoAsset, size string) []byte {
	key := a.ID + "/" + size
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.jpegs[key]; ok {
		return b
	}
	w := map[string]int{"preview": 1920, "fullsize": 3200, "frame": 480}[size]
	if w == 0 {
		w = 1920
	}
	h := w * 3 / 4
	if a.Portrait {
		w, h = h, w
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, scene(a.Kind, a.Seed, w, h), &jpeg.Options{Quality: 85})
	l.jpegs[key] = buf.Bytes()
	return l.jpegs[key]
}

// serve starts the fake Immich, on Immich's usual port where it is free.
func (l *demoLibrary) serve(t *testing.T) string {
	assetJSON := func(a demoAsset) map[string]any {
		m := map[string]any{
			"id": a.ID, "type": immich.Image, "checksum": "c-" + a.ID, "originalFileName": a.Name,
			"fileCreatedAt": a.Taken.Format(time.RFC3339), "localDateTime": a.Taken.Format(time.RFC3339), "duration": nil,
		}
		if a.Video {
			m["type"], m["duration"] = immich.Video, 14_000
		}
		return m
	}
	filterOf := func(r *http.Request) map[string]any {
		var body struct {
			Filter map[string]any `json:"filter"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		return body.Filter
	}
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/version", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]int{"major": 3, "minor": 4, "patch": 0})
	})
	mux.HandleFunc("GET /api/users/me", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"name": "Anna", "email": "anna@example.com"})
	})
	mux.HandleFunc("GET /api/albums", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("assetId")
		if id == "" {
			reply(w, l.albums)
			return
		}
		for _, a := range l.assets {
			if a.ID == id {
				reply(w, l.albumsOf(a))
				return
			}
		}
		reply(w, []immich.Album{})
	})
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) { reply(w, l.tags) })
	mux.HandleFunc("POST /api/search/statistics", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]int{"total": len(l.match(filterOf(r)))})
	})
	mux.HandleFunc("POST /api/search/metadata", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		for _, a := range l.match(filterOf(r)) {
			items = append(items, assetJSON(a))
		}
		reply(w, map[string]any{"assets": map[string]any{"items": items, "nextCursor": nil}})
	})
	mux.HandleFunc("GET /api/assets/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		for _, a := range l.assets {
			if a.ID == r.PathValue("id") {
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(l.photo(a, r.URL.Query().Get("size")))
				return
			}
		}
		http.NotFound(w, r)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:2283")
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
}

// demoFrame describes a fake frame and what an earlier sync left on it.
type demoFrame struct {
	place        string
	screen       store.Size
	rotate       bool
	caption      string
	limit        int
	albums, tags []string
	unsent       int   // the newest photos, added in Immich since the last sync
	stale        bool  // one photo on the frame is no longer selected
	foreign      int   // photos sent from a phone
	hidden       int   // photos hidden on the frame
	firstID      int64 // media ids start here
}

func (l *demoLibrary) addFrame(t *testing.T, grid *sdgtest.Grid, st *store.Store, dataDir string, d demoFrame) {
	t.Helper()
	fr := frameotest.New()
	fr.Info.Name, fr.Info.Placement = "Anna", d.place
	fr.Info.ScreenWidth, fr.Info.ScreenHeight = int32(d.screen.W), int32(d.screen.H)
	fr.Info.FrameCapabilities = &pb.FrameCapabilities{MaxVideoWidth: 1280, MaxVideoHeight: 720}
	fr.ListType, fr.GetMediaType = frameo.TypeGetAllMediaMetaData, frameo.TypeGetMedia
	fr.DeleteType, fr.VisibilityType = frameo.TypeDeleteMedia, frameo.TypeChangeMediaVisibility
	fr.PreviewBelow = 500
	fr.LiveLibrary = true
	fr.Servable = map[int64]frameotest.Servable{}
	peer, err := grid.AddDevice(func(tun *sdgtest.Tunnel) { _ = fr.Serve(tun) })
	if err != nil {
		t.Fatal(err)
	}

	f := store.NewFrame(sdg.PeerID(peer), "")
	f.Mode, f.Rotate, f.Caption, f.Limit = "relay", d.rotate, d.caption, d.limit
	f.Sources = store.Sources{Albums: d.albums, Tags: d.tags}
	learn(&f, fr.Info)
	if err := st.AddFrame(f); err != nil {
		t.Fatal(err)
	}

	filter := map[string]any{"type": map[string]any{"in": []any{immich.Image}}}
	var or []any
	if len(d.albums) > 0 {
		or = append(or, map[string]any{"albumIds": map[string]any{"any": anys(d.albums)}})
	}
	if len(d.tags) > 0 {
		or = append(or, map[string]any{"tagIds": map[string]any{"any": anys(d.tags)}})
	}
	filter["or"] = or
	wanted := l.match(filter)
	slices.SortFunc(wanted, func(a, b demoAsset) int { return b.Taken.Compare(a.Taken) })
	onFrame := wanted[d.unsent:]
	if d.stale {
		for _, a := range l.assets {
			if a.Albums[0] == "christmas" && !a.Video {
				onFrame = append(onFrame, a)
				break
			}
		}
	}

	m, err := syncplan.Load(manifestPath(dataDir, f.ID))
	if err != nil {
		t.Fatal(err)
	}
	id, sent := d.firstID, time.Now().Add(-26*time.Hour)
	add := func(a demoAsset, visible bool, received time.Time) {
		fr.Library = append(fr.Library, &pb.MediaMetaData{MediaId: id, IsVisible: visible, CaptureDate: a.Taken.UnixMilli(), ReceiveDate: received.UnixMilli()})
		frameCopy := l.photo(a, "frame")
		fr.Servable[id] = frameotest.Servable{Data: frameCopy, Preview: frameCopy, Extension: "jpg", CaptureDate: a.Taken.UnixMilli()}
		id++
	}
	for i, a := range onFrame {
		caption := ""
		if d.caption == "album" {
			caption = pickAlbum(l.albumsOf(a), d.albums)
		}
		m.Entries = append(m.Entries, syncplan.Entry{AssetID: a.ID, Checksum: "c-" + a.ID, Variant: "preview-jpg", Caption: caption,
			MediaID: id, Taken: a.Taken.UnixMilli(), Bytes: 380_000, SentAt: sent})
		hide := i%7 == 3 && i/7 < d.hidden // every seventh, as many as asked for
		add(a, !hide, sent.Add(time.Duration(-i)*time.Minute))
	}
	for i := range d.foreign {
		a := demoAsset{ID: fmt.Sprintf("phone-%d", i), Kind: []string{"garden", "beach", "mountain"}[i%3], Seed: 9000 + i, Taken: time.Now().Add(time.Duration(-i-2) * 24 * time.Hour)}
		add(a, true, time.Now().Add(time.Duration(-i-1)*time.Hour))
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
}

func anys(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// scene draws a simple landscape: a sky, a sun or moon, a far layer and the
// ground, in the colours of kind, varied by seed. Made-up photos with no one
// in them.
func scene(kind string, seed, w, h int) *image.RGBA {
	type palette struct{ skyTop, skyLow, sun, far, near, ground color.RGBA }
	p := map[string]palette{
		"beach":    {rgb(70, 140, 215), rgb(250, 214, 170), rgb(255, 236, 180), rgb(40, 110, 165), rgb(70, 150, 190), rgb(232, 204, 152)},
		"snow":     {rgb(18, 28, 68), rgb(92, 88, 140), rgb(240, 238, 220), rgb(62, 72, 104), rgb(180, 190, 210), rgb(236, 240, 248)},
		"garden":   {rgb(120, 180, 235), rgb(215, 232, 248), rgb(255, 230, 140), rgb(58, 112, 66), rgb(84, 140, 70), rgb(104, 162, 72)},
		"mountain": {rgb(92, 140, 205), rgb(236, 204, 178), rgb(255, 228, 190), rgb(96, 104, 136), rgb(66, 96, 86), rgb(84, 124, 82)},
	}[kind]
	r := rand.New(rand.NewPCG(uint64(seed), 7))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fw, fh := float64(w), float64(h)
	type cloud struct{ x, y, rx, ry float64 }
	var clouds []cloud
	if kind != "snow" {
		// The time of day, from noon blue to an evening glow, and some clouds.
		glow := r.Float64() * 0.65
		p.skyTop, p.skyLow = mix(p.skyTop, rgb(72, 64, 128), glow), mix(p.skyLow, rgb(250, 150, 100), glow)
		for range r.IntN(4) {
			clouds = append(clouds, cloud{r.Float64() * fw, (0.08 + 0.3*r.Float64()) * fh, (0.08 + 0.1*r.Float64()) * fw, (0.02 + 0.03*r.Float64()) * fh})
		}
	}
	sunX, sunY, sunR := r.Float64()*fw, (0.12+0.2*r.Float64())*fh, (0.04+0.04*r.Float64())*min(fw, fh)
	horizon := (0.5 + 0.15*r.Float64()) * fh
	ph1, ph2, f1, f2 := r.Float64()*6, r.Float64()*6, 3+4*r.Float64(), 2+3*r.Float64()
	for x := range w {
		fx := float64(x) / fw
		var far float64
		switch kind {
		case "beach":
			far = horizon - 0.01*fh*math.Sin(fx*40+ph1) // the sea
		case "mountain", "snow":
			tri := math.Abs(math.Mod(fx*f1+ph1, 1)-0.5) * 2 // peaks
			far = horizon - (0.08+0.22*tri)*fh
		default:
			far = horizon - (0.05+0.06*math.Sin(fx*f1*2+ph1))*fh // trees
		}
		near := horizon + 0.08*fh + 0.04*fh*math.Sin(fx*f2*2+ph2)
		for y := range h {
			fy := float64(y)
			var c color.RGBA
			switch {
			case fy < far:
				c = mix(p.skyTop, p.skyLow, fy/far)
				if dx, dy := float64(x)-sunX, fy-sunY; dx*dx+dy*dy < sunR*sunR {
					c = p.sun
				}
				for _, cl := range clouds {
					dx, dy := (float64(x)-cl.x)/cl.rx, (fy-cl.y)/cl.ry
					if d := dx*dx + dy*dy; d < 1 {
						c = mix(c, rgb(250, 248, 245), 0.85*(1-d))
					}
				}
			case fy < near:
				c = mix(p.far, p.near, (fy-far)/(near-far+1))
			default:
				c = mix(p.ground, rgb(p.ground.R/2, p.ground.G/2, p.ground.B/2), (fy-near)/(fh-near+1)*0.6)
			}
			img.SetRGBA(x, y, c)
		}
	}
	if kind == "snow" { // stars and snowflakes
		for range w * h / 900 {
			x, y := r.IntN(w), r.IntN(h)
			img.SetRGBA(x, y, rgb(250, 250, 255))
		}
	}
	return img
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

func mix(a, b color.RGBA, t float64) color.RGBA {
	t = max(0, min(1, t))
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return rgb(l(a.R, b.R), l(a.G, b.G), l(a.B, b.B))
}
