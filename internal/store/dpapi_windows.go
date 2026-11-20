// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// protect encrypts data for the current Windows user (DPAPI).
func protect(data []byte) ([]byte, error) {
	return dpapi(data, func(in, out *windows.DataBlob) error {
		return windows.CryptProtectData(in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, out)
	})
}

// unprotect decrypts what protect produced, as the same Windows user.
func unprotect(data []byte) ([]byte, error) {
	return dpapi(data, func(in, out *windows.DataBlob) error {
		return windows.CryptUnprotectData(in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, out)
	})
}

func dpapi(data []byte, call func(in, out *windows.DataBlob) error) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := call(&in, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
