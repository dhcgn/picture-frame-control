// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/frameo"
	"github.com/dhcgn/picture-frame-control/internal/frameo/frameotest"
	"github.com/dhcgn/picture-frame-control/internal/frameo/pb"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
	"github.com/dhcgn/picture-frame-control/internal/sdg/sdgtest"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

// withFakeFrame pairs a store with frame, served through a fake grid, and
// returns a FrameService that reaches it over the relay.
func withFakeFrame(t *testing.T, frame *frameotest.Frame) (*FrameService, *store.Store, store.Frame) {
	t.Helper()
	grid, err := sdgtest.NewGrid()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = grid.Close() })
	peer, err := grid.AddDevice(func(tun *sdgtest.Tunnel) { _ = frame.Serve(tun) })
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := store.NewFrame(sdg.PeerID(peer), "Test")
	f.Mode = "relay" // no mDNS in tests
	if err := st.AddFrame(f); err != nil {
		t.Fatal(err)
	}
	s := NewFrameService(st, t.TempDir())
	s.servers = []sdg.Endpoint{{Host: grid.Host(), Port: grid.Port()}}
	t.Cleanup(s.closeAll)
	return s, st, f
}

// TestFrameServiceAgainstFakeFrame drives the service the way the UI does --
// probe, list, thumbnails, delete -- against a fake frame behind a fake grid,
// and checks that all of it runs over one reused connection.
func TestFrameServiceAgainstFakeFrame(t *testing.T) {
	frame := frameotest.New()
	frame.ListType = frameo.TypeGetAllMediaMetaData
	frame.GetMediaType = frameo.TypeGetMedia
	frame.DeleteType = frameo.TypeDeleteMedia
	frame.PreviewBelow = 500
	frame.AsksWhoIsCalling = true // as a real frame does; counts the connections
	frame.Library = []*pb.MediaMetaData{
		{MediaId: 11, ReceiveDate: 1000, IsVisible: true},
		{MediaId: 22, ReceiveDate: 3000, IsVisible: true},
		{MediaId: 33, ReceiveDate: 2000, IsVisible: false},
	}
	preview := []byte("\xff\xd8\xffpreview-of-22")
	frame.Servable = map[int64]frameotest.Servable{
		22: {Data: []byte("\xff\xd8\xfforiginal-of-22"), Preview: preview, Extension: "jpg"},
	}

	s, st, f := withFakeFrame(t, frame)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	status, err := s.Probe(ctx, f.ID)
	if err != nil || !status.Online || status.Via != "relay" {
		t.Fatalf("Probe = %+v, %v", status, err)
	}
	if status.Count != 3 || !slices.Equal(status.Recent, []string{"22", "33", "11"}) {
		t.Errorf("Probe count/recent = %d %v, want 3 [22 33 11]", status.Count, status.Recent)
	}
	if got, _ := st.Frame(f.ID); got.Screen != (store.Size{W: 1280, H: 800}) {
		t.Errorf("screen not learned: %+v", got.Screen)
	}

	items, err := s.Media(ctx, f.ID)
	if err != nil || len(items) != 3 || items[0].ID != "22" || items[1].Visible {
		t.Fatalf("Media = %+v, %v", items, err)
	}

	thumbs := s.thumbMiddleware(http.NotFoundHandler())
	for range 2 { // the second request must come from the disk cache
		rec := httptest.NewRecorder()
		thumbs.ServeHTTP(rec, httptest.NewRequest("GET", "/thumb/"+f.ID+"/22", nil))
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != http.StatusOK || !bytes.Equal(body, preview) {
			t.Fatalf("thumb = %d %q, want the preview", rec.Code, body)
		}
	}
	if n := len(frame.Requests()); n != 1 {
		t.Errorf("frame asked for %d photos, want 1 (then cache)", n)
	}

	if err := s.Delete(ctx, f.ID, []string{"11"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(frame.Deleted(), []int64{11}) {
		t.Errorf("deleted %v, want [11]", frame.Deleted())
	}

	if n := len(frame.ClientNames()); n != 1 {
		t.Errorf("client introduced itself %d times, want 1: the connection should be reused", n)
	}
}
