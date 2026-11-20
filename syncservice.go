// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
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

	mu     sync.Mutex
	cancel context.CancelFunc // set while a sync runs
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
	return filepath.Join(s.dataDir, "manifests", frameID+".json")
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
			return errors.New("Die App darf die Bilder auf diesem Frame nicht sehen. Berechtigung im Frame anfragen")
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
		return nil, fmt.Errorf("Sync-Stand nicht lesbar: %w", err)
	}
	p.plan = syncplan.Compute(wanted, p.manifest.Entries, onFrame)
	return p, nil
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
// delete.
type RunFrame struct {
	FrameID       string   `json:"frameId"`
	DeleteForeign []string `json:"deleteForeign"`
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
		return nil, errors.New("Ein Sync läuft bereits")
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
	s.log(id, "info", "%s: %d bleiben, %d senden, %d löschen", p.frame.Name, len(p.plan.Keep), len(p.plan.Send), len(p.plan.Delete))

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
		s.log(id, "warn", "%d Bilder nicht gelöscht: Der App fehlt die Berechtigung „Verwalten“", len(del))
	case len(del) > 0:
		s.emit("sync:progress", SyncProgress{Frame: id, Phase: "delete", Total: len(del)})
		if _, err := s.frames.do(ctx, id, false, func(c *frameo.Client) error { return c.DeleteMedia(ctx, del) }); err != nil {
			res.Error = "Löschen fehlgeschlagen: " + err.Error()
			return res
		}
		m.Remove(del...)
		res.Deleted = len(del)
		s.log(id, "info", "%d Bilder gelöscht", len(del))
	}
	m.RemoveEntries(p.plan.Stale)
	if err := m.Save(); err != nil {
		res.Error = "Sync-Stand nicht speicherbar: " + err.Error()
		return res
	}

	c, err := s.immich.client()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	var sent []syncplan.Entry
	for i, a := range p.plan.Send {
		if ctx.Err() != nil {
			res.Cancelled = true
			s.log(id, "warn", "Abgebrochen. %d von %d gesendet", i, len(p.plan.Send))
			break
		}
		s.emit("sync:progress", SyncProgress{Frame: id, Phase: "send", Done: i, Total: len(p.plan.Send), Item: a.Name})
		path, variant, size, err := s.mediaFile(ctx, c, a)
		if err != nil {
			res.Failed++
			s.log(id, "error", "%s: Vorschau aus Immich nicht geladen: %v", a.Name, err)
			continue
		}
		var mediaID int64
		_, err = s.frames.do(ctx, id, false, func(fc *frameo.Client) (err error) {
			mediaID, err = fc.SendPhoto(ctx, frameo.Photo{Path: path, Taken: a.Taken, Fit: p.frame.Scale == "fit"})
			return err
		})
		if fe, ok := errors.AsType[*frameo.FrameError](err); ok && fe.Code == errFrameFull {
			res.Full = true
			s.log(id, "warn", "Der Frame ist voll. %d ältere Bilder nicht gesendet", len(p.plan.Send)-i)
			break
		}
		if err != nil {
			res.Failed++
			s.log(id, "error", "%s: Senden fehlgeschlagen: %v", a.Name, err)
			continue
		}
		e := syncplan.Entry{AssetID: a.ID, Checksum: a.Checksum, Variant: variant, MediaID: mediaID, Taken: a.Taken.UnixMilli(), Bytes: size, SentAt: time.Now().UTC()}
		m.Entries = append(m.Entries, e)
		if err := m.Save(); err != nil {
			res.Error = "Sync-Stand nicht speicherbar: " + err.Error()
			return res
		}
		sent = append(sent, e)
		res.Sent++
	}
	if len(sent) > 0 {
		s.emit("sync:progress", SyncProgress{Frame: id, Phase: "check"})
		s.reconcile(ctx, id, m, sent)
	}
	s.log(id, "info", "%s fertig: %d gesendet, %d gelöscht, %d fehlgeschlagen", p.frame.Name, res.Sent, res.Deleted, res.Failed)
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
		s.log(id, "warn", "ID-Abgleich nicht möglich: %v", err)
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
		s.log(id, "error", "Sync-Stand nicht speicherbar: %v", err)
	}
	level := "info"
	if matched+missing > 0 {
		level = "warn"
	}
	s.log(id, level, "ID-Abgleich: %d unter der gesendeten ID gefunden, %d über das Aufnahmedatum, %d nicht gefunden", direct, matched, missing)
}

// mediaFile returns a file ready to send for the asset: Immich's preview,
// kept in the cache so frames with the same settings share it.
func (s *SyncService) mediaFile(ctx context.Context, c *immich.Client, a syncplan.Asset) (path, variant string, size int, err error) {
	dir := filepath.Join(s.cacheDir, "media")
	base := a.ID + "_" + strings.NewReplacer("/", "_", "+", "-", "=", "").Replace(a.Checksum) + "_preview"
	if hit, _ := filepath.Glob(filepath.Join(dir, base+".*")); len(hit) > 0 {
		st, err := os.Stat(hit[0])
		if err == nil {
			return hit[0], "preview-" + strings.TrimPrefix(filepath.Ext(hit[0]), "."), int(st.Size()), nil
		}
	}
	data, ext, err := c.Preview(ctx, a.ID)
	if err != nil {
		return "", "", 0, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", 0, err
	}
	path = filepath.Join(dir, base+"."+ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", "", 0, err
	}
	return path, "preview-" + cmp.Or(ext, "jpg"), len(data), nil
}
