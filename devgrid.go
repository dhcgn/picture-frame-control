// SPDX-License-Identifier: GPL-3.0-or-later

//go:build server

package main

import (
	"net"
	"os"
	"strconv"

	"github.com/dhcgn/picture-frame-control/internal/sdg"
)

// In server builds, which serve the UI to a browser for development and are
// never released, PFC_DEV_GRID=host:port points the app at a fake Frameo grid.
// The screenshots use it (screenshots_test.go).
func init() {
	host, port, err := net.SplitHostPort(os.Getenv("PFC_DEV_GRID"))
	if p, perr := strconv.Atoi(port); err == nil && perr == nil {
		devServers = []sdg.Endpoint{{Host: host, Port: p}}
	}
}
