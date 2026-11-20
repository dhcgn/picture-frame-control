// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // sizes for imageSize
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
	"github.com/dhcgn/picture-frame-control/internal/i18n"
	"github.com/dhcgn/picture-frame-control/internal/immich"
	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

// errFrameFull is the code a frame answers with when its storage is full.
// It is not in the app's protobuf enum; it has been seen in the wild.
const errFrameFull pb.Error_Code = 12

// SyncService mirrors each frame's albums and tags onto it (S-02..S-11).
type SyncService struct {
	store    *store.Store
	frames   *FrameService
	immich   *ImmichService
	dataDir  string // manifests live in dataDir/manifests
	cacheDir string // prepared media lives in cacheDir/media
	emit     func(name string, data any)

	mu       sync.Mutex
	cancel   context.CancelFunc        // set while a sync runs
	albumsOf map[string][]immich.Album // asset id -> its albums, for captions
}

// SyncProgress is emitted as "sync:progress".
type SyncProgress struct {
	Frame string `json:"frame"`
	Phase string `json:"phase"` // "prepare", "delete", "send", "check", "done"
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Item  string `json:"item"`
}

// SyncLog is emitted as "sync:log".
type SyncLog struct {
	Time  string `json:"time"`
	Frame string `json:"frame"`
	Level string `json:"level"` // "info", "warn", "error"
	Text  string `json:"text"`
}

func (s *SyncService) log(frame, level, format string, args ...any) {
	s.emit("sync:log", SyncLog{Time: time.Now().Format("15:04:05"), Frame: frame, Level: level, Text: fmt.Sprintf(format, args...)})
}

func (s *SyncService) manifestPath(frameID string) string {
	return manifestPath(s.dataDir, frameID)
}

// manifestPath is where the record of what this app sent to a frame lives.
func manifestPath(dataDir, frameID string) string {
	return filepath.Join(dataDir, "manifests", frameID+".json")
}

// FramePlan is what the next sync would do on one frame.
type FramePlan struct {
	FrameID   string   `json:"frameId"`
	Error     string   `json:"error"` // why this frame cannot sync now
	Keep      int      `json:"keep"`
	Send      int      `json:"send"`
	Delete    int      `json:"delete"`
	Foreign   []string `json:"foreign"` // photos on the frame this app did not send
	Skipped   int      `json:"skipped"` // videos, until they are supported
	OverLimit int      `json:"overLimit"`
	CanDelete bool     `json:"canDelete"`
}

type prepared struct {
	frame     store.Frame
	wanted    []syncplan.Asset // what belongs on the frame, newest first, within the limit
	plan      syncplan.Plan
	manifest  *syncplan.Manifest
	skipped   int
	overLimit int
	canManage bool
}

// prepare works out the plan for one frame from Immich, the frame itself and
// the manifest.
func (s *SyncService) prepare(ctx context.Context, id string) (*prepared, error) {
	f, err := s.store.Frame(id)
	if err != nil {
		return nil, err
	}
	c, err := s.immich.client()
	if err != nil {
		return nil, err
	}
	assets, err := c.Assets(ctx, f.Sources.Albums, f.Sources.Tags)
	if err != nil {
		return nil, err
	}
	p := &prepared{frame: f}
	var wanted []syncplan.Asset
	for _, a := range assets {
		if a.Type != immich.Image {
			p.skipped++ // ponytail: videos need the video+thumb transfer (M5)
			continue
		}
		wanted = append(wanted, syncplan.Asset{ID: a.ID, Checksum: a.Checksum, Taken: takenAt(a), Name: a.OriginalFileName})
	}
	if f.Caption == "album" {
		if err := s.albumCaptions(ctx, c, wanted, f.Sources.Albums); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(wanted, func(a, b syncplan.Asset) int { return b.Taken.Compare(a.Taken) })
	if f.Limit > 0 && len(wanted) > f.Limit {
		p.overLimit = len(wanted) - f.Limit
		wanted = wanted[:f.Limit]
	}

	onFrame := map[int64]bool{}
	if _, err := s.frames.do(ctx, id, true, func(fc *frameo.Client) error {
		info, err := fc.GetInfo(ctx)
		if err != nil {
			return err
		}
		if !frameo.Granted(info, frameo.PermissionView) {
			return errors.New(i18n.T("Die App darf die Bilder auf diesem Frame nicht sehen. Berechtigung im Frame anfragen",
				"The app may not see the photos on this frame. Request the permission in the frame"))
		}
		p.canManage = frameo.Granted(info, frameo.PermissionManage)
		items, err := fc.ListMedia(ctx)
		for _, m := range items {
			onFrame[m.GetMediaId()] = true
		}
		return err
	}); err != nil {
		return nil, err
	}

	if p.manifest, err = syncplan.Load(s.manifestPath(id)); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("Sync-Stand nicht lesbar", "Sync record unreadable"), err)
	}
	p.wanted = wanted
	p.plan = syncplan.Compute(wanted, p.manifest.Entries, onFrame)
	return p, nil
}

// albumCaptions gives each asset the name of an Immich album it is in,
// however it was selected (album or tag). Immich answers that per asset, so
// the answers are kept for the session.
func (s *SyncService) albumCaptions(ctx context.Context, c *immich.Client, assets []syncplan.Asset, selected []string) error {
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := range assets {
		wg.Go(func() {
			s.mu.Lock()
			albums, ok := s.albumsOf[assets[i].ID]
			s.mu.Unlock()
			if !ok {
				sem <- struct{}{}
				var err error
				albums, err = c.AlbumsOf(ctx, assets[i].ID)
				<-sem
				if err != nil {
					mu.Lock()
					firstErr = cmp.Or(firstErr, err)
					mu.Unlock()
					return
				}
				// ponytail: session cache; an album renamed in Immich shows after a restart
				s.mu.Lock()
				if s.albumsOf == nil {
					s.albumsOf = map[string][]immich.Album{}
				}
				s.albumsOf[assets[i].ID] = albums
				s.mu.Unlock()
			}
			assets[i].Caption = pickAlbum(albums, selected)
		})
	}
	wg.Wait()
	if firstErr != nil {
		return fmt.Errorf("%s: %w", i18n.T("Albumnamen für die Bildunterschrift nicht geladen", "Could not load album names for the captions"), firstErr)
	}
	return nil
}

// pickAlbum chooses the album to caption a photo with: one of the frame's
// own albums if the photo is in one, else any; among several, the smallest,
// which is usually the more specific ("Hochzeit Anna" rather than "Best of").
func pickAlbum(albums []immich.Album, selected []string) string {
	best := -1
	for i, a := range albums {
		if best < 0 {
			best = i
			continue
		}
		in, bestIn := slices.Contains(selected, a.ID), slices.Contains(selected, albums[best].ID)
		if in && !bestIn || in == bestIn && a.AssetCount < albums[best].AssetCount {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return albums[best].Name
}

// takenAt is when the photo was taken, as the frame should file it.
func takenAt(a immich.Asset) time.Time {
	for _, s := range []string{a.FileCreatedAt, a.LocalDateTime} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Preview works out, without changing anything, what a sync would do on
// each frame.
func (s *SyncService) Preview(ctx context.Context, frameIDs []string) []FramePlan {
	out := make([]FramePlan, len(frameIDs))
	for i, id := range frameIDs {
		out[i] = FramePlan{FrameID: id}
		p, err := s.prepare(ctx, id)
		if err != nil {
			out[i].Error = err.Error()
			continue
		}
		out[i].Keep, out[i].Send, out[i].Delete = len(p.plan.Keep), len(p.plan.Send), len(p.plan.Delete)
		out[i].Skipped, out[i].OverLimit, out[i].CanDelete = p.skipped, p.overLimit, p.canManage
		for _, m := range p.plan.Foreign {
			out[i].Foreign = append(out[i].Foreign, strconv.FormatInt(m, 10))
		}
	}
	return out
}

// RunFrame is one frame to sync, with the foreign photos the user agreed to
// delete. Force replaces every photo this app sent, even unchanged ones, so
// they are prepared afresh with the current settings.
type RunFrame struct {
	FrameID       string   `json:"frameId"`
	DeleteForeign []string `json:"deleteForeign"`
	Force         bool     `json:"force"`
}

// FrameResult is what a sync did on one frame.
type FrameResult struct {
	FrameID   string `json:"frameId"`
	Sent      int    `json:"sent"`
	Deleted   int    `json:"deleted"`
	Failed    int    `json:"failed"`
	Full      bool   `json:"full"`
	Cancelled bool   `json:"cancelled"`
	Error     string `json:"error"`
}

// Run syncs the frames one after another and returns what happened on
// each. Progress and a log are emitted as events while it runs.
func (s *SyncService) Run(ctx context.Context, frames []RunFrame) ([]FrameResult, error) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil, errors.New(i18n.T("Ein Sync läuft bereits", "A sync is already running"))
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	// ponytail: frames run one after another; one goroutine per frame if the total time hurts
	out := make([]FrameResult, 0, len(frames))
	for _, rf := range frames {
		r := s.runFrame(ctx, rf)
		if r.Error != "" {
			s.log(rf.FrameID, "error", "%s", r.Error)
		}
		s.emit("sync:progress", SyncProgress{Frame: rf.FrameID, Phase: "done", Done: r.Sent, Total: r.Sent})
		out = append(out, r)
	}
	return out, nil
}

// Cancel stops a running sync after the photo in transfer. What was sent
// stays recorded.
func (s *SyncService) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *SyncService) runFrame(ctx context.Context, rf RunFrame) FrameResult {
	id := rf.FrameID
	res := FrameResult{FrameID: id}
	s.emit("sync:progress", SyncProgress{Frame: id, Phase: "prepare"})
	p, err := s.prepare(ctx, id)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	m := p.manifest
	if rf.Force {
		// Without "manage" the old copies would stay next to the new ones.
		if !p.canManage {
			res.Error = i18n.T("Alles neu senden braucht die Berechtigung „Verwalten“", "Resending everything needs the \"manage\" permission")
			return res
		}
		s.log(id, "info", i18n.T("Alles neu senden: %d Bilder dieser App werden ersetzt", "Resending everything: %d photos from this app are replaced"), len(p.plan.Keep))
		p.plan.Delete = append(p.plan.Delete, p.plan.Keep...)
		p.plan.Keep, p.plan.Send = nil, p.wanted
	}
	s.log(id, "info", i18n.T("%s: %d bleiben, %d senden, %d löschen", "%s: %d stay, %d to send, %d to delete"),
		p.frame.DisplayName(), len(p.plan.Keep), len(p.plan.Send), len(p.plan.Delete))

	// Delete first: it makes room for what follows.
	var del []int64
	for _, e := range p.plan.Delete {
		del = append(del, e.MediaID)
	}
	foreign := map[int64]bool{}
	for _, f := range p.plan.Foreign {
		foreign[f] = true
	}
	for _, f := range rf.DeleteForeign {
		// Only photos that are foreign now, never something sent since the preview.
		if n, err := strconv.ParseInt(f, 10, 64); err == nil && foreign[n] {
			del = append(del, n)
		}
	}
	switch {
	case len(del) > 0 && !p.canManage:
		s.log(id, "warn", i18n.T("%d Bilder nicht gelöscht: Der App fehlt die Berechtigung „Verwalten“",
			"%d photos not deleted: the app lacks the \"manage\" permission"), len(del))
	case len(del) > 0:
		s.emit("sync:progress", SyncProgress{Frame: id, Phase: "delete", Total: len(del)})
		if _, err := s.frames.do(ctx, id, false, func(c *frameo.Client) error { return c.DeleteMedia(ctx, del) }); err != nil {
			res.Error = i18n.T("Löschen fehlgeschlagen: ", "Deleting failed: ") + err.Error()
			return res
		}
		m.Remove(del...)
		res.Deleted = len(del)
		s.log(id, "info", i18n.T("%d Bilder gelöscht", "%d photos deleted"), len(del))
	}
	m.RemoveEntries(p.plan.Stale)
	if err := m.Save(); err != nil {
		res.Error = i18n.T("Sync-Stand nicht speicherbar: ", "Could not save the sync record: ") + err.Error()
		return res
	}

	c, err := s.immich.client()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if len(p.plan.Send) > 0 {
		if w, h, _, ok := s.shrinkTo(p.frame); ok {
			s.log(id, "info", i18n.T("ImageMagick verkleinert die Bilder auf %d×%d (%s)", "ImageMagick shrinks the photos to %d×%d (%s)"), w, h, strings.ToUpper(shrinkFormat))
		}
	}
	var sent []syncplan.Entry
	for i, a := range p.plan.Send {
		if ctx.Err() != nil {
			res.Cancelled = true
			s.log(id, "warn", i18n.T("Abgebrochen. %d von %d gesendet", "Cancelled. %d of %d sent"), i, len(p.plan.Send))
			break
		}
		s.emit("sync:progress", SyncProgress{Frame: id, Phase: "send", Done: i, Total: len(p.plan.Send), Item: a.Name})
		path, variant, size, err := s.mediaFile(ctx, c, a, p.frame)
		if err != nil {
			res.Failed++
			s.log(id, "error", i18n.T("%s: Bild aus Immich nicht geladen: %v", "%s: could not load the photo from Immich: %v"), a.Name, err)
			continue
		}
		var mediaID int64
		_, err = s.frames.do(ctx, id, false, func(fc *frameo.Client) (err error) {
			mediaID, err = fc.SendPhoto(ctx, frameo.Photo{Path: path, Taken: a.Taken, Fit: p.frame.Scale == "fit", Caption: a.Caption})
			return err
		})
		if fe, ok := errors.AsType[*frameo.FrameError](err); ok && fe.Code == errFrameFull {
			res.Full = true
			s.log(id, "warn", i18n.T("Der Frame ist voll. %d ältere Bilder nicht gesendet", "The frame is full. %d older photos not sent"), len(p.plan.Send)-i)
			break
		}
		if err != nil {
			res.Failed++
			s.log(id, "error", i18n.T("%s: Senden fehlgeschlagen: %v", "%s: sending failed: %v"), a.Name, err)
			continue
		}
		e := syncplan.Entry{AssetID: a.ID, Checksum: a.Checksum, Variant: variant, Caption: a.Caption, MediaID: mediaID, Taken: a.Taken.UnixMilli(), Bytes: size, SentAt: time.Now().UTC()}
		m.Entries = append(m.Entries, e)
		if err := m.Save(); err != nil {
			res.Error = i18n.T("Sync-Stand nicht speicherbar: ", "Could not save the sync record: ") + err.Error()
			return res
		}
		sent = append(sent, e)
		res.Sent++
	}
	if len(sent) > 0 {
		s.emit("sync:progress", SyncProgress{Frame: id, Phase: "check"})
		s.reconcile(ctx, id, m, sent)
	}
	s.log(id, "info", i18n.T("%s fertig: %d gesendet, %d gelöscht, %d fehlgeschlagen", "%s done: %d sent, %d deleted, %d failed"),
		p.frame.DisplayName(), res.Sent, res.Deleted, res.Failed)
	return res
}

// reconcile checks that the photos just sent are listed on the frame under
// the ids this client gave them. Where a frame files them under its own ids,
// they are matched by the capture date this client set (open question O-2 in
// docs/anforderungen.md).
func (s *SyncService) reconcile(ctx context.Context, id string, m *syncplan.Manifest, sent []syncplan.Entry) {
	var items []*pb.MediaMetaData
	if _, err := s.frames.do(ctx, id, true, func(c *frameo.Client) (err error) {
		items, err = c.ListMedia(ctx)
		return err
	}); err != nil {
		s.log(id, "warn", i18n.T("ID-Abgleich nicht möglich: %v", "Could not check the ids: %v"), err)
		return
	}
	listed := map[int64]bool{}
	byTaken := map[int64][]int64{}
	known := map[int64]bool{}
	for _, e := range m.Entries {
		known[e.MediaID] = true
	}
	for _, it := range items {
		listed[it.GetMediaId()] = true
		if !known[it.GetMediaId()] {
			byTaken[it.GetCaptureDate()] = append(byTaken[it.GetCaptureDate()], it.GetMediaId())
		}
	}
	direct, matched, missing := 0, 0, 0
	for _, e := range sent {
		if listed[e.MediaID] {
			direct++
			continue
		}
		if cands := byTaken[e.Taken]; len(cands) > 0 {
			i := slices.IndexFunc(m.Entries, func(x syncplan.Entry) bool { return x.AssetID == e.AssetID && x.MediaID == e.MediaID })
			m.Entries[i].MediaID = cands[0]
			byTaken[e.Taken] = cands[1:]
			matched++
			continue
		}
		missing++
	}
	if err := m.Save(); err != nil {
		s.log(id, "error", i18n.T("Sync-Stand nicht speicherbar: %v", "Could not save the sync record: %v"), err)
	}
	level := "info"
	if matched+missing > 0 {
		level = "warn"
	}
	s.log(id, level, i18n.T("ID-Abgleich: %d unter der gesendeten ID gefunden, %d über das Aufnahmedatum, %d nicht gefunden",
		"ID check: %d found under the id sent, %d by capture date, %d not found"), direct, matched, missing)
}

// shrinkTo is the screen ImageMagick shrinks photos for on this frame and the
// variant that makes, or ok false when photos go as Immich's preview: without
// ImageMagick, or before the frame reported its screen.
func (s *SyncService) shrinkTo(f store.Frame) (w, h int, variant string, ok bool) {
	w, h = screenOf(f)
	if s.store.Config().Magick == "" || w == 0 || h == 0 {
		return 0, 0, "", false
	}
	mode := "cover"
	if f.Scale == "fit" {
		mode = "fit"
	}
	return w, h, fmt.Sprintf("%s-%dx%d-%s-q%d", shrinkFormat, w, h, mode, shrinkQuality), true
}

// screenOf is the frame's screen as it hangs: a frame mounted portrait swaps
// width and height. Zero until the frame has reported it.
func screenOf(f store.Frame) (w, h int) {
	if f.Rotate {
		return f.Screen.H, f.Screen.W
	}
	return f.Screen.W, f.Screen.H
}

// covers reports whether a w×h photo fills a sw×sh screen without being
// enlarged: in both directions where the frame crops, in one where it fits.
func covers(w, h, sw, sh int, fit bool) bool {
	if fit {
		return w >= sw || h >= sh
	}
	return w >= sw && h >= sh
}

// imageSize reads the size of a JPEG, PNG or GIF from its header.
func imageSize(path string) (w, h int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	c, _, err := image.DecodeConfig(f)
	return c.Width, c.Height, err == nil
}

// mediaFile returns a file ready to send for the asset: the smallest rendition
// Immich has that fills the frame's screen, shrunk to that screen when
// ImageMagick is set up. All of it stays in the cache, so frames with the same
// screen and scaling share it.
// ponytail: the cache only grows; a "clear cache" button (E-04) when it gets big
func (s *SyncService) mediaFile(ctx context.Context, c *immich.Client, a syncplan.Asset, f store.Frame) (path, variant string, size int, err error) {
	dir := filepath.Join(s.cacheDir, "media")
	base := filepath.Join(dir, a.ID+"_"+strings.NewReplacer("/", "_", "+", "-", "=", "").Replace(a.Checksum))
	w, h, shrunk, ok := s.shrinkTo(f)
	out := base + "_" + shrunk + "." + shrinkFormat
	if st, err := os.Stat(out); ok && err == nil {
		return out, shrunk, int(st.Size()), nil
	}

	path, variant, size, err = s.source(ctx, c, a.ID, base, f)
	if err != nil || !ok {
		return path, variant, size, err
	}
	if err := shrink(ctx, s.store.Config().Magick, path, out, w, h, f.Scale == "fit"); err != nil {
		if ctx.Err() != nil {
			return "", "", 0, ctx.Err()
		}
		s.log(f.ID, "warn", i18n.T("%s: ImageMagick fehlgeschlagen, sende das Bild aus Immich unverändert: %v",
			"%s: ImageMagick failed, sending the photo from Immich as it is: %v"), a.Name, err)
		return path, variant, size, nil
	}
	st, err := os.Stat(out)
	if err != nil {
		return "", "", 0, err
	}
	return out, shrunk, int(st.Size()), nil
}

// source returns the smallest rendition from Immich that fills the frame's
// screen without the frame enlarging it: the preview, or the full-size image
// where the preview falls short -- a portrait photo on a landscape Full HD
// frame, say, which the preview's 1440 pixels cannot cover. A preview whose
// size cannot be read counts as falling short.
func (s *SyncService) source(ctx context.Context, c *immich.Client, id, base string, f store.Frame) (path, variant string, size int, err error) {
	path, variant, size, err = s.fetch(ctx, c, id, base, "preview")
	if err != nil {
		return "", "", 0, err
	}
	sw, sh := screenOf(f)
	if w, h, ok := imageSize(path); sw == 0 || ok && covers(w, h, sw, sh, f.Scale == "fit") {
		return path, variant, size, nil
	}
	return s.fetch(ctx, c, id, base, "fullsize")
}

// fetch returns a rendition of the asset ("preview" or "fullsize") from the
// cache, downloading it the first time.
func (s *SyncService) fetch(ctx context.Context, c *immich.Client, id, base, rendition string) (path, variant string, size int, err error) {
	base += "_" + rendition
	if hit, _ := filepath.Glob(base + ".*"); len(hit) > 0 {
		if st, err := os.Stat(hit[0]); err == nil {
			return hit[0], rendition + "-" + strings.TrimPrefix(filepath.Ext(hit[0]), "."), int(st.Size()), nil
		}
	}
	data, ext, err := c.Thumbnail(ctx, id, rendition)
	if err != nil {
		return "", "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		return "", "", 0, err
	}
	path = base + "." + ext
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", "", 0, err
	}
	return path, rendition + "-" + cmp.Or(ext, "jpg"), len(data), nil
}
