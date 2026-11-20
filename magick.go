// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/dhcgn/picture-frame-control/internal/i18n"
)

// What ImageMagick makes of a photo (M-02). magick picks the format by the
// file extension, so "jpg" here is all it takes if a frame cannot show WebP
// (open question O-3).
// ponytail: fixed quality; a setting (E-03) when someone asks for one
const (
	shrinkFormat  = "webp"
	shrinkQuality = 82
)

// magickCmd runs ImageMagick without flashing a console window.
func magickCmd(ctx context.Context, magick string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, magick, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

// magickVersion checks that magick is ImageMagick and returns its version,
// e.g. "7.1.2-35".
func magickVersion(ctx context.Context, magick string) (string, error) {
	out, err := magickCmd(ctx, magick, "-version").Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("ImageMagick lässt sich nicht starten", "ImageMagick does not start"), err)
	}
	// "Version: ImageMagick 7.1.2-35 Q16-HDRI x64 ..."
	f := strings.Fields(string(out))
	if len(f) < 3 || f[0] != "Version:" || f[1] != "ImageMagick" {
		return "", errors.New(i18n.T("Das ist kein ImageMagick", "This is not ImageMagick"))
	}
	return f[2], nil
}

// findMagick looks for magick.exe on the PATH, then where ImageMagick's
// installer puts it. It returns "" when there is none.
func findMagick() string {
	if p, err := exec.LookPath("magick"); err == nil {
		return p
	}
	hits, _ := filepath.Glob(filepath.Join(os.Getenv("ProgramFiles"), "ImageMagick-*", "magick.exe"))
	if len(hits) == 0 {
		return ""
	}
	return hits[len(hits)-1] // the newest, as far as names sort
}

// shrink writes in to out at no more than a w×h screen needs: covering it when
// the frame crops, inside it when it fits. It never enlarges and never crops,
// so the frame still crops around its own centre point.
func shrink(ctx context.Context, magick, in, out string, w, h int, fit bool) error {
	geometry := fmt.Sprintf("%dx%d^>", w, h)
	if fit {
		geometry = fmt.Sprintf("%dx%d>", w, h)
	}
	tmp := out + ".tmp." + shrinkFormat // a crash leaves no half file under the real name
	msg, err := magickCmd(ctx, magick, in, "-auto-orient", "-resize", geometry, "-quality", strconv.Itoa(shrinkQuality), tmp).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		if m := bytes.TrimSpace(msg); len(m) > 0 {
			return fmt.Errorf("%w: %s", err, m)
		}
		return err
	}
	return os.Rename(tmp, out)
}
