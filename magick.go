// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/dhcgn/picture-frame-control/internal/i18n"
	"github.com/dhcgn/picture-frame-control/internal/store"
)

// tool is an external program the app can use when it is set up: ImageMagick
// for photos (M-02), ffmpeg for videos (M-03).
type tool struct {
	label  string                      // for messages
	exe    string                      // its name on the PATH
	banner string                      // what -version prints before the version number
	places []string                    // where installers put it, as globs
	path   func(*store.Config) *string // where the config keeps its path
}

var tools = map[string]tool{
	"magick": {"ImageMagick", "magick", "Version: ImageMagick",
		[]string{`%ProgramFiles%\ImageMagick-*\magick.exe`},
		func(c *store.Config) *string { return &c.Magick }},
	"ffmpeg": {"ffmpeg", "ffmpeg", "ffmpeg version",
		[]string{`%ProgramFiles%\ffmpeg\bin\ffmpeg.exe`, `C:\ffmpeg\bin\ffmpeg.exe`, `C:\tools\ffmpeg.exe`, `C:\tools\ffmpeg*\bin\ffmpeg.exe`},
		func(c *store.Config) *string { return &c.FFmpeg }},
}

// toolIDs is the order the settings show the tools in.
var toolIDs = []string{"magick", "ffmpeg"}

// hiddenCmd runs a console program without flashing a console window.
func hiddenCmd(ctx context.Context, exe string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

// version checks that path is this tool and returns its version, e.g.
// "7.1.2-35".
func (t tool) version(ctx context.Context, path string) (string, error) {
	out, err := hiddenCmd(ctx, path, "-version").Output()
	if err != nil {
		return "", fmt.Errorf(i18n.T("%s lässt sich nicht starten: %w", "%s does not start: %w"), t.label, err)
	}
	rest, ok := strings.CutPrefix(string(out), t.banner+" ")
	f := strings.Fields(rest)
	if !ok || len(f) == 0 {
		return "", fmt.Errorf(i18n.T("Das ist kein %s", "This is not %s"), t.label)
	}
	return f[0], nil
}

// find looks for the tool on the PATH, then where its installers put it. It
// returns "" when there is none.
func (t tool) find() string {
	if p, err := exec.LookPath(t.exe); err == nil {
		return p
	}
	for _, g := range t.places {
		hits, _ := filepath.Glob(strings.ReplaceAll(g, "%ProgramFiles%", os.Getenv("ProgramFiles")))
		if len(hits) > 0 {
			return hits[len(hits)-1] // the newest, as far as names sort
		}
	}
	return ""
}

// What ImageMagick makes of a photo (M-02). magick picks the format by the
// file extension, so "jpg" here is all it takes if a frame cannot show WebP
// (open question O-3).
// ponytail: fixed quality; a setting (E-03) when someone asks for one
const (
	shrinkFormat  = "webp"
	shrinkQuality = 82
)

// shrink writes in to out at no more than a w×h screen needs: covering it when
// the frame crops, inside it when it fits. It never enlarges and never crops,
// so the frame still crops around its own centre point.
func shrink(ctx context.Context, magick, in, out string, w, h int, fit bool) error {
	geometry := fmt.Sprintf("%dx%d^>", w, h)
	if fit {
		geometry = fmt.Sprintf("%dx%d>", w, h)
	}
	tmp := out + ".tmp." + shrinkFormat // a crash leaves no half file under the real name
	return run(hiddenCmd(ctx, magick, in, "-auto-orient", "-resize", geometry, "-quality", strconv.Itoa(shrinkQuality), tmp), tmp, out)
}

// run runs cmd, which writes tmp, and renames tmp to out once it succeeded.
// The error carries what the program said.
func run(cmd *exec.Cmd, tmp, out string) error {
	msg, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		if m := bytes.TrimSpace(msg); len(m) > 0 {
			return fmt.Errorf("%w: %s", err, m)
		}
		return err
	}
	return os.Rename(tmp, out)
}
