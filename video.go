// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/i18n"
	"github.com/dhcgn/picture-frame-control/internal/immich"
	"github.com/dhcgn/picture-frame-control/internal/store"
	"github.com/dhcgn/picture-frame-control/internal/syncplan"
)

// What Frameo accepts for videos, from its recommendations: 4 s to 4:59 min,
// up to 125 MB, 30 fps, 2-5 Mbps on 7" and 10" frames (720p), 2-10 Mbps on
// 15" frames (1080p), 96 kbps sound.
const (
	videoMin      = 4 * time.Second
	videoMax      = 4*time.Minute + 59*time.Second
	videoMaxBytes = 125_000_000
	videoCRF      = 23
)

// videoBox is the largest a video may be on this frame: its screen as it
// hangs, within the size the frame says it can play. Unknown screens get
// 720p, which every Frameo frame plays.
func videoBox(f store.Frame) (w, h int) {
	w, h = screenOf(f)
	if w == 0 || h == 0 {
		w, h = 1280, 720
	}
	long, short := max(f.MaxVideo.W, f.MaxVideo.H), min(f.MaxVideo.W, f.MaxVideo.H)
	if long > 0 && short > 0 {
		if w >= h {
			w, h = min(w, long), min(h, short)
		} else {
			w, h = min(w, short), min(h, long)
		}
	}
	return w, h
}

// videoRate is the most video bitrate to spend: Frameo's ceiling for the
// frame's size, lowered so that the whole video stays under 125 MB.
func videoRate(w, h int, length time.Duration) int {
	rate := 5_000_000
	if max(w, h) > 1280 {
		rate = 10_000_000
	}
	d := min(cmp.Or(length, videoMax), videoMax)
	budget := int(float64(videoMaxBytes)*8*0.95/d.Seconds()) - 96_000 // 5 % for the container
	return min(rate, budget)
}

// transcode turns any video into one the frame plays: H.264 and AAC in MP4,
// fitting inside w×h and never enlarged, at most 30 fps, cut at 4:59, upright
// as recorded.
// ponytail: HDR videos (iPhone HLG) come out flat; tone mapping when someone has them
func transcode(ctx context.Context, ffmpeg, in, out string, w, h int, rate int) error {
	tmp := out + ".tmp.mp4"
	return run(hiddenCmd(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-i", in,
		"-t", strconv.Itoa(int(videoMax.Seconds())),
		"-map", "0:v:0", "-map", "0:a:0?",
		"-vf", fmt.Sprintf("scale=w='min(%d,iw)':h='min(%d,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", w, h),
		"-fpsmax", "30",
		"-c:v", "libx264", "-preset", "medium", "-crf", strconv.Itoa(videoCRF), "-profile:v", "high", "-pix_fmt", "yuv420p",
		"-maxrate", strconv.Itoa(rate), "-bufsize", strconv.Itoa(2*rate),
		"-c:a", "aac", "-b:a", "96k", "-ac", "2",
		"-movflags", "+faststart", tmp), tmp, out)
}

// still takes the picture the frame shows before the video plays: one second
// in, where a video rarely is still black.
func still(ctx context.Context, ffmpeg, video, out string) error {
	tmp := out + ".tmp.jpg"
	return run(hiddenCmd(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-ss", "1", "-i", video,
		"-frames:v", "1", "-q:v", "3", tmp), tmp, out)
}

// videoFile returns a video ready to send and its still, converted with
// ffmpeg from the original in Immich and kept in the cache.
// ponytail: the original is downloaded again for a frame with another screen; keep it if that hurts
func (s *SyncService) videoFile(ctx context.Context, c *immich.Client, a syncplan.Asset, f store.Frame) (video, thumb, variant string, size int, err error) {
	ffmpeg := s.store.Config().FFmpeg
	w, h := videoBox(f)
	variant = fmt.Sprintf("mp4-%dx%d-crf%d", w, h, videoCRF)
	base := s.cacheBase(a) + "_" + variant
	video, thumb = base+".mp4", base+".jpg"
	if st, err := os.Stat(video); err == nil {
		if _, err := os.Stat(thumb); err == nil {
			return video, thumb, variant, int(st.Size()), nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		return "", "", "", 0, err
	}
	orig, err := os.CreateTemp(filepath.Dir(base), "original-*")
	if err != nil {
		return "", "", "", 0, err
	}
	defer os.Remove(orig.Name())
	err = c.Original(ctx, a.ID, orig)
	if cerr := orig.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", "", 0, err
	}
	if a.Length > videoMax {
		s.log(f.ID, "info", i18n.T("%s ist länger als 4:59 und wird gekürzt", "%s is longer than 4:59 and is cut"), a.Name)
	}
	if err := transcode(ctx, ffmpeg, orig.Name(), video, w, h, videoRate(w, h, a.Length)); err != nil {
		return "", "", "", 0, fmt.Errorf("ffmpeg: %w", err)
	}
	st, err := os.Stat(video)
	if err != nil {
		return "", "", "", 0, err
	}
	if st.Size() > videoMaxBytes {
		_ = os.Remove(video)
		return "", "", "", 0, fmt.Errorf(i18n.T("umgewandelt %d MB groß, Frameo nimmt höchstens 125 MB", "%d MB once converted; Frameo takes at most 125 MB"), st.Size()/1_000_000)
	}
	if err := still(ctx, ffmpeg, video, thumb); err != nil {
		return "", "", "", 0, fmt.Errorf("ffmpeg: %w", err)
	}
	return video, thumb, variant, int(st.Size()), nil
}
