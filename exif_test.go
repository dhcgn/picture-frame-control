// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// withOrientation inserts an EXIF APP1 segment with the orientation tag after
// the JPEG's SOI marker.
func withOrientation(t *testing.T, jpg []byte, o uint16) []byte {
	t.Helper()
	var tiff bytes.Buffer
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(42))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD0 offset
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(1)) // one entry
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(3)) // SHORT
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(1))
	_ = binary.Write(&tiff, binary.LittleEndian, o)
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0)) // no next IFD
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	out := []byte{0xff, 0xd8, 0xff, 0xe1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// TestImageSizeHonoursExifOrientation: a 400×300 photo stored sideways reads
// as 300×400, so smart mode fits it as the portrait photo it is.
func TestImageSizeHonoursExifOrientation(t *testing.T) {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 400, 300)), nil)
	for o, want := range map[uint16][2]int{1: {400, 300}, 3: {400, 300}, 6: {300, 400}, 8: {300, 400}} {
		path := filepath.Join(t.TempDir(), "p.jpg")
		if err := os.WriteFile(path, withOrientation(t, buf.Bytes(), o), 0o600); err != nil {
			t.Fatal(err)
		}
		if w, h, ok := imageSize(path); !ok || w != want[0] || h != want[1] {
			t.Errorf("orientation %d: %d×%d ok=%t, want %d×%d", o, w, h, ok, want[0], want[1])
		}
	}
}
