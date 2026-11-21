// SPDX-License-Identifier: GPL-3.0-or-later

package immich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestAssetLength reads a video's running time in both forms Immich has used:
// milliseconds (current) and "h:mm:ss.ffffff" (older), and null for photos.
func TestAssetLength(t *testing.T) {
	for in, want := range map[string]time.Duration{
		`5000`:             5 * time.Second,
		`299500`:           4*time.Minute + 59500*time.Millisecond,
		`null`:             0, // a photo
		`"0:00:05.000000"`: 5 * time.Second,
		`"1:02:03.000000"`: time.Hour + 2*time.Minute + 3*time.Second,
		`""`:               0,
		`{"odd":1}`:        0, // unknown, not an error
	} {
		var a Asset
		if err := json.Unmarshal([]byte(`{"id":"x","duration":`+in+`}`), &a); err != nil || a.Length() != want {
			t.Errorf("duration %s: %s, %v; want %s", in, a.Length(), err, want)
		}
	}
}

func TestAssetsPagesRetriesAndFilters(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("x-api-key") != "k" || r.URL.Path != "/api/search/metadata" {
			t.Errorf("request %s with key %q", r.URL.Path, r.Header.Get("x-api-key"))
		}
		if n == 1 { // a hiccup the client must ride out
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Filter map[string]any
			Cursor string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		or, _ := body.Filter["or"].([]any)
		if len(or) != 2 || body.Filter["trashedAt"] == nil {
			t.Errorf("filter = %v, want two or-branches and a trash exclusion", body.Filter)
		}
		if body.Cursor == "" {
			_, _ = w.Write([]byte(`{"assets":{"items":[{"id":"a"},{"id":"b"}],"nextCursor":"next"}}`))
		} else {
			_, _ = w.Write([]byte(`{"assets":{"items":[{"id":"c","type":"VIDEO"}],"nextCursor":null}}`))
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL+"/api/", "k") // a pasted /api suffix is tolerated
	if err != nil {
		t.Fatal(err)
	}
	c.retryWait = 0
	got, err := c.Assets(context.Background(), []string{"album"}, []string{"tag"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Type != Video {
		t.Errorf("Assets = %+v, want a, b, c(video)", got)
	}
	if calls.Load() != 3 {
		t.Errorf("%d requests, want 3 (one retried)", calls.Load())
	}
}

// A frame without sources must mirror nothing, never the whole library.
func TestNoSourcesMeansNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "k")
	if n, err := c.Count(context.Background(), nil, nil); n != 0 || err != nil {
		t.Errorf("Count = %d, %v", n, err)
	}
	if a, err := c.Assets(context.Background(), nil, []string{}, false); a != nil || err != nil {
		t.Errorf("Assets = %v, %v", a, err)
	}
}

// People are asked for only on request and decoded the way Immich 3.4 sends
// them: an unnamed face has an empty name, a missing birth date is null.
func TestAssetsWithPeople(t *testing.T) {
	var asked []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ WithPeople bool }
		_ = json.NewDecoder(r.Body).Decode(&body)
		asked = append(asked, body.WithPeople)
		_, _ = w.Write([]byte(`{"assets":{"items":[{"id":"a","people":[
			{"id":"p1","name":"Maria","birthDate":"2018-06-20","isHidden":false},
			{"id":"p2","name":"","birthDate":null,"isHidden":false}]},{"id":"b"}],"nextCursor":null}}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "k")
	for _, with := range []bool{false, true} {
		got, err := c.Assets(context.Background(), []string{"album"}, nil, with)
		if err != nil {
			t.Fatal(err)
		}
		if len(got[0].People) != 2 || got[0].People[0] != (Person{ID: "p1", Name: "Maria", BirthDate: "2018-06-20"}) || got[0].People[1].BirthDate != "" || got[1].People != nil {
			t.Errorf("People = %+v", got)
		}
	}
	if len(asked) != 2 || asked[0] || !asked[1] {
		t.Errorf("withPeople sent as %v, want false then true", asked)
	}
}

func TestRejectedKeyIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "bad")
	_, err := c.Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API-Key") || calls.Load() != 1 {
		t.Errorf("err = %v after %d calls, want one call and a key error", err, calls.Load())
	}
}

func TestNewRejectsBadAddress(t *testing.T) {
	for _, s := range []string{"", "photos.lan", "ftp://x"} {
		if _, err := New(s, "k"); err == nil {
			t.Errorf("New(%q) accepted", s)
		}
	}
}
