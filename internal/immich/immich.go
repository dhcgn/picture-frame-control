// SPDX-License-Identifier: GPL-3.0-or-later

// Package immich is a small client for the parts of the Immich API this app
// needs: albums, tags, and finding the assets in them. Requires Immich 3.2 or
// later, which introduced the composable search filter.
package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/i18n"
)

// Client talks to one Immich server with one API key.
type Client struct {
	base      string // server origin + "/api"
	key       string
	hc        *http.Client
	retryWait time.Duration // first backoff; doubles per attempt
}

// New checks the address and key and returns a client. It does not contact
// the server.
func New(server, apiKey string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New(i18n.T("Die Server-Adresse muss mit http:// oder https:// beginnen",
			"The server address must start with http:// or https://"))
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New(i18n.T("Der API-Key fehlt", "The API key is missing"))
	}
	base := strings.TrimSuffix(strings.TrimRight(u.String(), "/"), "/api")
	return &Client{
		base:      base + "/api",
		key:       strings.TrimSpace(apiKey),
		hc:        &http.Client{Timeout: 60 * time.Second},
		retryWait: time.Second,
	}, nil
}

// StatusError is an HTTP error answer from Immich.
type StatusError struct {
	Code    int
	Path    string
	Message string
}

func (e *StatusError) Error() string {
	switch e.Code {
	case http.StatusUnauthorized:
		return i18n.T("Immich lehnt den API-Key ab", "Immich rejects the API key")
	case http.StatusForbidden:
		return fmt.Sprintf(i18n.T("Dem API-Key fehlt eine Berechtigung für %s: %s", "The API key lacks a permission for %s: %s"), e.Path, e.Message)
	case http.StatusNotFound:
		return fmt.Sprintf(i18n.T("Immich kennt %s nicht. Stimmt die Server-Adresse?", "Immich does not know %s. Is the server address right?"), e.Path)
	}
	return fmt.Sprintf(i18n.T("Immich antwortet auf %s mit %d: %s", "Immich answers %s with %d: %s"), e.Path, e.Code, e.Message)
}

// call sends one request and decodes the JSON answer into out. Rate limits,
// server errors and network failures are retried twice with backoff; any
// other error answer is final.
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("x-api-key", c.key)
		req.Header.Set("Accept", "application/json")
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		hc := c.hc
		stream, streaming := out.(*streamBody)
		if streaming {
			hc = &http.Client{Transport: c.hc.Transport} // no overall timeout; ctx ends it
		}
		resp, err := hc.Do(req)
		if err == nil {
			if resp.StatusCode < 300 {
				defer resp.Body.Close()
				if out == nil {
					return nil
				}
				if streaming {
					_, err = io.Copy(stream.w, resp.Body)
					return err
				}
				if raw, ok := out.(*rawBody); ok {
					raw.contentType = resp.Header.Get("Content-Type")
					raw.data, err = io.ReadAll(resp.Body)
					return err
				}
				if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
					return fmt.Errorf(i18n.T("Antwort von Immich auf %s nicht lesbar: %w", "Immich's answer to %s is unreadable: %w"), path, err)
				}
				return nil
			}
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			var e struct{ Message any }
			if json.Unmarshal(msg, &e) == nil && e.Message != nil {
				msg = fmt.Append(nil, e.Message)
			}
			err = &StatusError{Code: resp.StatusCode, Path: path, Message: strings.TrimSpace(string(msg))}
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return err
			}
		}
		if attempt == 2 || ctx.Err() != nil {
			if _, ok := errors.AsType[*StatusError](err); ok {
				return err
			}
			return fmt.Errorf("%s: %w", i18n.T("Immich nicht erreichbar", "Immich not reachable"), err)
		}
		select {
		case <-time.After(c.retryWait << attempt):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// rawBody makes call hand back the answer's bytes instead of decoding JSON.
type rawBody struct {
	data        []byte
	contentType string
}

// Thumbnail downloads a rendition of an asset and returns the bytes and the
// file extension that fits them. size is "preview" (1440 pixels on the short
// side by default, already upright, JPEG unless the server is set otherwise)
// or "fullsize": the original for formats a browser shows, else a full-size
// JPEG where the server makes one, else the preview again.
func (c *Client) Thumbnail(ctx context.Context, id, size string) ([]byte, string, error) {
	var r rawBody
	if err := c.call(ctx, http.MethodGet, "/assets/"+url.PathEscape(id)+"/thumbnail?size="+url.QueryEscape(size), nil, &r); err != nil {
		return nil, "", err
	}
	ext := "jpg"
	for _, e := range []string{"webp", "png", "gif", "avif"} {
		if strings.Contains(r.contentType, e) {
			ext = e
		}
	}
	return r.data, ext, nil
}

// Version is the server's version.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v is major.minor or newer.
func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || v.Major == major && v.Minor >= minor
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	return v, c.call(ctx, http.MethodGet, "/server/version", nil, &v)
}

// User is the owner of the API key.
type User struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	return u, c.call(ctx, http.MethodGet, "/users/me", nil, &u)
}

// Album is an album the key's owner can see, own or shared.
type Album struct {
	ID         string `json:"id"`
	Name       string `json:"albumName"`
	AssetCount int    `json:"assetCount"`
	Shared     bool   `json:"shared"`
}

func (c *Client) Albums(ctx context.Context) ([]Album, error) {
	var a []Album
	return a, c.call(ctx, http.MethodGet, "/albums", nil, &a)
}

// AlbumsOf lists the albums an asset is in.
func (c *Client) AlbumsOf(ctx context.Context, assetID string) ([]Album, error) {
	var a []Album
	return a, c.call(ctx, http.MethodGet, "/albums?assetId="+url.QueryEscape(assetID), nil, &a)
}

// Tag is one tag; Value is its full path, e.g. "Familie/Kinder".
type Tag struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	ParentID string `json:"parentId"`
}

func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	var t []Tag
	return t, c.call(ctx, http.MethodGet, "/tags", nil, &t)
}

// Asset is what the sync needs to know about one photo or video.
type Asset struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"` // IMAGE or VIDEO
	Checksum         string   `json:"checksum"`
	LocalDateTime    string   `json:"localDateTime"` // wall-clock time where taken, marked as UTC
	FileCreatedAt    string   `json:"fileCreatedAt"` // the moment it was taken, UTC
	OriginalFileName string   `json:"originalFileName"`
	Duration         duration `json:"duration"`
	People           []Person `json:"people"` // only filled by Assets with people=true
}

// Person is someone Immich recognised in a photo. Name is empty for an
// unnamed face cluster; BirthDate is "YYYY-MM-DD" or empty.
type Person struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BirthDate string `json:"birthDate"`
	Hidden    bool   `json:"isHidden"`
}

// Length is a video's running time, zero when Immich does not say.
func (a Asset) Length() time.Duration { return time.Duration(a.Duration) }

// duration is a running time as Immich states it: milliseconds in current
// versions, "h:mm:ss.ffffff" in older ones, null for photos. Anything else
// counts as unknown rather than spoiling the whole answer.
type duration time.Duration

func (d *duration) UnmarshalJSON(b []byte) error {
	var ms float64
	if json.Unmarshal(b, &ms) == nil {
		*d = duration(ms * float64(time.Millisecond))
		return nil
	}
	var s string
	var h, m int
	var sec float64
	if json.Unmarshal(b, &s) == nil {
		if n, _ := fmt.Sscanf(s, "%d:%d:%f", &h, &m, &sec); n == 3 {
			*d = duration(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)))
		}
	}
	return nil
}

// Original writes an asset's original file to w. A video can take minutes,
// so only ctx limits how long it may take.
func (c *Client) Original(ctx context.Context, id string, w io.Writer) error {
	return c.call(ctx, http.MethodGet, "/assets/"+url.PathEscape(id)+"/original", nil, &streamBody{w})
}

// streamBody makes call copy the answer to w instead of decoding JSON.
type streamBody struct{ w io.Writer }

// Media types a frame can show.
const (
	Image = "IMAGE"
	Video = "VIDEO"
)

// filter selects what a frame mirrors: assets in any of the albums or
// carrying any of the tags, on the timeline (not archived, hidden or
// locked) and not in the trash. It returns nil when there are no sources:
// an empty filter would match the whole library.
func filter(albums, tags []string, types []string) map[string]any {
	var or []map[string]any
	if len(albums) > 0 {
		or = append(or, map[string]any{"albumIds": map[string]any{"any": albums}})
	}
	if len(tags) > 0 {
		or = append(or, map[string]any{"tagIds": map[string]any{"any": tags}})
	}
	if len(or) == 0 {
		return nil
	}
	if len(types) == 0 {
		types = []string{Image, Video}
	}
	f := map[string]any{
		"type":       map[string]any{"in": types},
		"visibility": map[string]any{"eq": "timeline"},
		"trashedAt":  map[string]any{"eq": nil},
	}
	if len(or) == 1 {
		maps.Copy(f, or[0])
	} else {
		f["or"] = or
	}
	return f
}

// Count is how many assets of the given types (default: images and videos)
// the albums and tags hold together, each asset counted once.
func (c *Client) Count(ctx context.Context, albums, tags []string, types ...string) (int, error) {
	f := filter(albums, tags, types)
	if f == nil {
		return 0, nil
	}
	var r struct{ Total int }
	return r.Total, c.call(ctx, http.MethodPost, "/search/statistics", map[string]any{"filter": f}, &r)
}

// Assets lists the images and videos the albums and tags hold together, each
// once, following Immich's paging to the end. With people, each asset also
// carries the people assigned to it.
func (c *Client) Assets(ctx context.Context, albums, tags []string, people bool) ([]Asset, error) {
	f := filter(albums, tags, nil)
	if f == nil {
		return nil, nil
	}
	var all []Asset
	cursor := ""
	for {
		body := map[string]any{"filter": f, "size": 1000}
		if people {
			body["withPeople"] = true
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		var r struct {
			Assets struct {
				Items      []Asset `json:"items"`
				NextCursor *string `json:"nextCursor"`
			} `json:"assets"`
		}
		if err := c.call(ctx, http.MethodPost, "/search/metadata", body, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Assets.Items...)
		if r.Assets.NextCursor == nil || *r.Assets.NextCursor == "" {
			return all, nil
		}
		cursor = *r.Assets.NextCursor
	}
}
