// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
	"github.com/dhcgn/picture-frame-control/internal/i18n"
	"github.com/dhcgn/picture-frame-control/internal/link"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

// FrameService manages paired frames and what is on them.
//
// Talking to a frame starts with mDNS discovery and an encrypted handshake,
// which takes seconds, so connections are kept open per frame and reused,
// one operation at a time, until they sit idle for idleClose.
type FrameService struct {
	store    *store.Store
	cacheDir string
	dataDir  string // where the sync manifests live, to tell synced photos from foreign ones

	mu    sync.Mutex
	conns map[string]*frameConn

	servers []sdg.Endpoint // overrides Frameo's grid servers; tests only
}

type frameConn struct {
	mu   sync.Mutex
	c    *frameo.Client
	via  link.Via
	idle *time.Timer
}

const idleClose = 90 * time.Second

func NewFrameService(st *store.Store, cacheDir string) *FrameService {
	return &FrameService{store: st, cacheDir: cacheDir, conns: map[string]*frameConn{}}
}

// ServiceShutdown closes all open frame connections when the app quits.
func (s *FrameService) ServiceShutdown() error {
	s.closeAll()
	return nil
}

func (s *FrameService) closeAll() {
	s.mu.Lock()
	conns := s.conns
	s.conns = map[string]*frameConn{}
	s.mu.Unlock()
	for _, cn := range conns {
		cn.mu.Lock()
		cn.drop()
		cn.mu.Unlock()
	}
}

func (s *FrameService) closeConn(id string) {
	s.mu.Lock()
	cn := s.conns[id]
	delete(s.conns, id)
	s.mu.Unlock()
	if cn != nil {
		cn.mu.Lock()
		cn.drop()
		cn.mu.Unlock()
	}
}

// drop closes the connection. The caller holds cn.mu.
func (cn *frameConn) drop() {
	if cn.idle != nil {
		cn.idle.Stop()
	}
	if cn.c != nil {
		_ = cn.c.Close()
		cn.c = nil
	}
}

// do runs fn on an open connection to frame id, opening one if needed. A
// failed connection is dropped so the next call reconnects; a read on a
// reused connection is retried once, because the relay quietly ends sessions
// that sat idle.
func (s *FrameService) do(ctx context.Context, id string, read bool, fn func(*frameo.Client) error) (link.Via, error) {
	f, err := s.store.Frame(id)
	if err != nil {
		return "", err
	}
	peer, err := sdg.ParseKey(f.ID)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	cn := s.conns[id]
	if cn == nil {
		cn = &frameConn{}
		s.conns[id] = cn
	}
	s.mu.Unlock()

	cn.mu.Lock()
	defer cn.mu.Unlock()
	for attempt := 0; ; attempt++ {
		reused := cn.c != nil
		if cn.c == nil {
			o, err := s.linkOptions(f)
			if err != nil {
				return "", err
			}
			octx, cancel := context.WithTimeout(ctx, 45*time.Second)
			c, via, err := link.Open(octx, peer, o)
			cancel()
			if err != nil {
				return "", err
			}
			cn.c, cn.via = c, via
		}
		err := fn(cn.c)
		var refused *frameo.FrameError
		if err == nil || errors.As(err, &refused) {
			if cn.idle == nil {
				cn.idle = time.AfterFunc(idleClose, func() { cn.mu.Lock(); cn.drop(); cn.mu.Unlock() })
			} else {
				cn.idle.Reset(idleClose)
			}
			return cn.via, explain(err)
		}
		cn.drop()
		if !read || !reused || attempt > 0 || ctx.Err() != nil {
			return "", err
		}
	}
}

func (s *FrameService) linkOptions(f store.Frame) (link.Options, error) {
	id, err := s.store.Identity()
	if err != nil {
		return link.Options{}, err
	}
	return link.Options{Identity: id, Name: s.store.Config().ClientName, Mode: link.Mode(f.Mode), Servers: s.servers}, nil
}

// explain turns the frame's refusals into sentences for the UI.
func explain(err error) error {
	var fe *frameo.FrameError
	if !errors.As(err, &fe) {
		return err
	}
	switch fe.Code {
	case pb.Error_MISSING_PERMISSION:
		return errors.New(i18n.T("Dafür fehlt die Berechtigung. In den Frame-Einstellungen anfragen und am Frame erlauben",
			"Permission missing. Request it in the frame settings and allow it on the frame"))
	case pb.Error_MISSING_MEDIA_ITEM:
		return errors.New(i18n.T("Das Bild ist nicht mehr auf dem Frame", "The photo is no longer on the frame"))
	case pb.Error_DECLINED:
		return errors.New(i18n.T("Am Frame abgelehnt", "Declined on the frame"))
	case pb.Error_UNAUTHORIZED:
		return errors.New(i18n.T("Der Frame kennt diese App nicht mehr. Bitte neu koppeln", "The frame no longer knows this app. Please pair again"))
	}
	return err
}

// List returns the paired frames.
func (s *FrameService) List() []store.Frame {
	return s.store.Config().Frames
}

// FrameStatus is what a frame reports about itself right now.
type FrameStatus struct {
	Online    bool     `json:"online"`
	Via       string   `json:"via"`
	Error     string   `json:"error"`
	CanView   bool     `json:"canView"`
	CanManage bool     `json:"canManage"`
	Count     int      `json:"count"`  // photos on the frame, -1 when not allowed to look
	Recent    []string `json:"recent"` // ids of the newest photos, for the card collage
}

// Probe connects to the frame, refreshes what is stored about it (screen
// size, video limits) and reports its state. An unreachable frame is not an
// error: it comes back with Online false and the reason.
func (s *FrameService) Probe(ctx context.Context, id string) (FrameStatus, error) {
	st := FrameStatus{Count: -1}
	via, err := s.do(ctx, id, true, func(c *frameo.Client) error {
		info, err := c.GetInfo(ctx)
		if err != nil {
			return err
		}
		st.CanView = frameo.Granted(info, frameo.PermissionView)
		st.CanManage = frameo.Granted(info, frameo.PermissionManage)
		if err := s.store.UpdateFrame(id, func(f *store.Frame) { learn(f, info) }); err != nil {
			return err
		}
		if !st.CanView {
			return nil
		}
		items, err := c.ListMedia(ctx)
		if err != nil {
			return err
		}
		sortNewestFirst(items)
		st.Count = len(items)
		for _, m := range items[:min(5, len(items))] {
			st.Recent = append(st.Recent, strconv.FormatInt(m.GetMediaId(), 10))
		}
		return nil
	})
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	st.Online, st.Via = true, string(via)
	return st, nil
}

// learn copies what the frame says about itself into its stored record.
func learn(f *store.Frame, info *pb.FrameInfo) {
	f.FrameName = info.GetName()
	f.Place = info.GetPlacement()
	f.Screen = store.Size{W: int(info.GetScreenWidth()), H: int(info.GetScreenHeight())}
	caps := info.GetFrameCapabilities()
	f.MaxVideo = store.Size{W: int(caps.GetMaxVideoWidth()), H: int(caps.GetMaxVideoHeight())}
}

func sortNewestFirst(items []*pb.MediaMetaData) {
	slices.SortFunc(items, func(a, b *pb.MediaMetaData) int {
		return cmp.Compare(b.GetReceiveDate(), a.GetReceiveDate())
	})
}

// Discovered is a frame found on the local network.
type Discovered struct {
	Instance string `json:"instance"`
	Host     string `json:"host"`
	PairedID string `json:"pairedId"` // empty when not paired with this app
}

// Discover lists the frames advertising themselves on the local network.
func (s *FrameService) Discover(ctx context.Context) ([]Discovered, error) {
	found, err := link.Discover(ctx, link.Options{Discover: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	frames := s.store.Config().Frames
	out := make([]Discovered, 0, len(found))
	for _, f := range found {
		d := Discovered{Instance: f.Instance, Host: f.Endpoint.Host}
		for _, fr := range frames {
			if peer, err := sdg.ParseKey(fr.ID); err == nil && sdg.IsLocalInstance(f.Instance, peer) {
				d.PairedID = fr.ID
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// Pair pairs with the frame showing code. Without a name, the app follows the
// name the frame reports.
func (s *FrameService) Pair(ctx context.Context, code, name string) (store.Frame, error) {
	id, err := s.store.Identity()
	if err != nil {
		return store.Frame{}, err
	}
	pctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	peer, err := link.Pair(pctx, code, link.Options{Identity: id, Name: s.store.Config().ClientName})
	if err != nil {
		return store.Frame{}, err
	}
	f := store.NewFrame(peer, strings.TrimSpace(name))
	if err := s.store.AddFrame(f); err != nil {
		return store.Frame{}, err
	}
	// Learn the frame's name, place and screen size right away.
	_, _ = s.do(ctx, f.ID, true, func(c *frameo.Client) error {
		info, err := c.GetInfo(ctx)
		if err != nil {
			return err
		}
		return s.store.UpdateFrame(f.ID, func(sf *store.Frame) { learn(sf, info) })
	})
	return s.store.Frame(f.ID)
}

// FrameSettings are the per-frame options the user edits.
type FrameSettings struct {
	Name    string `json:"name"` // empty follows the frame's own name
	Limit   int    `json:"limit"`
	Scale   string `json:"scale"`
	Video   string `json:"video"`
	Rotate  bool   `json:"rotate"`
	Mode    string `json:"mode"`
	Caption string `json:"caption"`
}

// UpdateSettings saves a frame's options.
func (s *FrameService) UpdateSettings(id string, fs FrameSettings) (store.Frame, error) {
	name := strings.TrimSpace(fs.Name)
	switch {
	case fs.Caption != "" && fs.Caption != "album":
		return store.Frame{}, fmt.Errorf(i18n.T("unbekannte Bildunterschrift %q", "unknown caption %q"), fs.Caption)
	case fs.Limit < 0:
		return store.Frame{}, errors.New(i18n.T("Das Limit darf nicht negativ sein", "The limit must not be negative"))
	case fs.Scale != "crop" && fs.Scale != "fit" && fs.Scale != "smart":
		return store.Frame{}, fmt.Errorf(i18n.T("unbekannte Darstellung %q", "unknown display mode %q"), fs.Scale)
	case fs.Video != "" && fs.Video != "standard" && fs.Video != "compact":
		return store.Frame{}, fmt.Errorf(i18n.T("unbekannte Videoqualität %q", "unknown video quality %q"), fs.Video)
	case fs.Mode != "auto" && fs.Mode != "local" && fs.Mode != "relay":
		return store.Frame{}, fmt.Errorf(i18n.T("unbekannter Verbindungsweg %q", "unknown connection mode %q"), fs.Mode)
	}
	old, err := s.store.Frame(id)
	if err != nil {
		return store.Frame{}, err
	}
	if err := s.store.UpdateFrame(id, func(f *store.Frame) {
		f.Name, f.Limit, f.Scale, f.Video, f.Rotate, f.Mode, f.Caption = name, fs.Limit, fs.Scale, fs.Video, fs.Rotate, fs.Mode, fs.Caption
	}); err != nil {
		return store.Frame{}, err
	}
	if old.Mode != fs.Mode {
		s.closeConn(id)
	}
	return s.store.Frame(id)
}

// Forget removes a frame from this app. The photos stay on the frame.
func (s *FrameService) Forget(id string) error {
	s.closeConn(id)
	if err := s.store.RemoveFrame(id); err != nil {
		return err
	}
	return os.RemoveAll(s.thumbDir(id))
}

// RequestPermission asks the frame's owner to allow viewing, or managing,
// photos, and waits until someone answers at the frame or three minutes pass.
func (s *FrameService) RequestPermission(ctx context.Context, id string, manage bool) error {
	p := frameo.PermissionView
	if manage {
		p = frameo.PermissionManage
	}
	wctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, err := s.do(wctx, id, false, func(c *frameo.Client) error { return c.RequestPermission(wctx, p) })
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New(i18n.T("Am Frame wurde in drei Minuten nicht geantwortet", "Nobody answered on the frame within three minutes"))
	}
	return err
}

// MediaItem is one photo or video on the frame. Ids travel as strings: they
// are 64-bit and JavaScript numbers are not.
type MediaItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "picture", "video" or "greeting"
	Visible  bool   `json:"visible"`
	Captured int64  `json:"captured"` // unix ms
	Received int64  `json:"received"` // unix ms
	Synced   bool   `json:"synced"`   // sent by this app's sync; false means foreign
	Caption  string `json:"caption"`  // the caption the sync sent with it
}

// Media lists what is on the frame, newest first, and marks the photos the
// sync put there: the manifest records the frame's id for each of them.
func (s *FrameService) Media(ctx context.Context, id string) ([]MediaItem, error) {
	var items []*pb.MediaMetaData
	_, err := s.do(ctx, id, true, func(c *frameo.Client) (err error) {
		items, err = c.ListMedia(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	sortNewestFirst(items)
	synced := map[int64]syncplan.Entry{}
	if m, err := syncplan.Load(manifestPath(s.dataDir, id)); err == nil {
		for _, e := range m.Entries {
			synced[e.MediaID] = e
		}
	}
	out := make([]MediaItem, len(items))
	for i, m := range items {
		e, ok := synced[m.GetMediaId()]
		t := "picture"
		switch m.GetType() {
		case pb.MediaMetaData_Type_MEDIAMETADATA_TYPE_VIDEO:
			t = "video"
		case pb.MediaMetaData_Type_MEDIAMETADATA_TYPE_GREETING:
			t = "greeting"
		}
		out[i] = MediaItem{
			ID:       strconv.FormatInt(m.GetMediaId(), 10),
			Type:     t,
			Visible:  m.GetIsVisible(),
			Captured: m.GetCaptureDate(),
			Received: m.GetReceiveDate(),
			Synced:   ok,
			Caption:  e.Caption,
		}
	}
	return out, nil
}

func parseIDs(ids []string) ([]int64, error) {
	out := make([]int64, len(ids))
	for i, s := range ids {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(i18n.T("ungültige Bild-ID %q", "invalid photo id %q"), s)
		}
		out[i] = n
	}
	return out, nil
}

// SetVisible hides or shows photos without removing them.
func (s *FrameService) SetVisible(ctx context.Context, id string, mediaIDs []string, visible bool) error {
	ids, err := parseIDs(mediaIDs)
	if err != nil {
		return err
	}
	_, err = s.do(ctx, id, false, func(c *frameo.Client) error { return c.SetMediaVisible(ctx, ids, visible) })
	return err
}

// Delete removes photos from the frame for good.
func (s *FrameService) Delete(ctx context.Context, id string, mediaIDs []string) error {
	ids, err := parseIDs(mediaIDs)
	if err != nil {
		return err
	}
	if _, err := s.do(ctx, id, false, func(c *frameo.Client) error { return c.DeleteMedia(ctx, ids) }); err != nil {
		return err
	}
	for _, m := range mediaIDs {
		if old, _ := filepath.Glob(filepath.Join(s.thumbDir(id), m+".*")); len(old) > 0 {
			_ = os.Remove(old[0])
		}
	}
	return nil
}

// Download asks for a folder and copies the photos' originals into it. It
// returns the folder, or "" when the user cancelled.
func (s *FrameService) Download(ctx context.Context, id string, mediaIDs []string) (string, error) {
	ids, err := parseIDs(mediaIDs)
	if err != nil {
		return "", err
	}
	dir, err := application.Get().Dialog.OpenFile().
		SetTitle(i18n.T("Ordner für die Bilder wählen", "Choose a folder for the photos")).
		CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil || dir == "" {
		return "", err
	}
	var errs []error
	for _, mid := range ids {
		var d *frameo.Download
		if _, err := s.do(ctx, id, true, func(c *frameo.Client) (err error) {
			d, err = c.GetMedia(ctx, frameo.Fetch{ID: mid})
			return err
		}); err != nil {
			errs = append(errs, fmt.Errorf(i18n.T("Bild %d: %w", "Photo %d: %w"), mid, err))
			continue
		}
		name := fmt.Sprintf("%d.%s", mid, d.Extension())
		if ms := d.Media.GetCaptureDate(); ms > 0 {
			name = time.UnixMilli(ms).UTC().Format("2006-01-02_150405_") + name
		}
		if err := os.WriteFile(filepath.Join(dir, name), d.Data, 0o644); err != nil {
			errs = append(errs, err)
		}
	}
	return dir, errors.Join(errs...)
}

// SendProgress is emitted as "send:progress" while files are sent.
type SendProgress struct {
	Frame string `json:"frame"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	File  string `json:"file"`
}

// SendFiles asks for image files and sends them to the frame. It returns how
// many arrived; files that failed are reported together in the error.
func (s *FrameService) SendFiles(ctx context.Context, id string, fit bool, caption string) (int, error) {
	paths, err := application.Get().Dialog.OpenFile().
		SetTitle(i18n.T("Bilder zum Senden wählen", "Choose photos to send")).
		AddFilter(i18n.T("Bilder", "Photos"), "*.jpg;*.jpeg;*.png;*.webp;*.gif;*.bmp;*.heic").
		PromptForMultipleSelection()
	if err != nil || len(paths) == 0 {
		return 0, err
	}
	sent := 0
	var errs []error
	for i, p := range paths {
		application.Get().Event.Emit("send:progress", SendProgress{Frame: id, Done: i, Total: len(paths), File: filepath.Base(p)})
		if _, err := s.do(ctx, id, false, func(c *frameo.Client) error {
			_, err := c.SendPhoto(ctx, frameo.Photo{Path: p, Fit: fit, Caption: caption})
			return err
		}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		sent++
	}
	application.Get().Event.Emit("send:progress", SendProgress{Frame: id, Done: len(paths), Total: len(paths)})
	return sent, errors.Join(errs...)
}

func (s *FrameService) thumbDir(id string) string {
	return filepath.Join(s.cacheDir, "thumbs", id)
}

// thumbMiddleware serves /thumb/<frame id>/<media id>: the frame's small
// stored copy of a photo, fetched once and then kept on disk. Being plain
// image URLs, the grid can use the browser's own lazy loading.
func (s *FrameService) thumbMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, "/thumb/")
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		id, mediaID, ok := strings.Cut(rest, "/")
		mid, err := strconv.ParseInt(mediaID, 10, 64)
		if !ok || err != nil || filepath.Base(id) != id {
			http.NotFound(w, r)
			return
		}
		dir := s.thumbDir(id)
		if cached, _ := filepath.Glob(filepath.Join(dir, mediaID+".*")); len(cached) > 0 {
			http.ServeFile(w, r, cached[0])
			return
		}
		var d *frameo.Download
		if _, err := s.do(r.Context(), id, true, func(c *frameo.Client) (err error) {
			d, err = c.GetMedia(r.Context(), frameo.Fetch{ID: mid, Size: frameo.SizePreview})
			return err
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		path := filepath.Join(dir, mediaID+"."+d.Extension())
		if err := os.MkdirAll(dir, 0o700); err == nil {
			_ = os.WriteFile(path, d.Data, 0o600)
		}
		w.Header().Set("Content-Type", http.DetectContentType(d.Data))
		_, _ = w.Write(d.Data)
	})
}
