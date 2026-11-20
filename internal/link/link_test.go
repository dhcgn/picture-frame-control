// SPDX-License-Identifier: GPL-3.0-or-later

package link_test

import (
	"context"
	"testing"
	"time"

	"github.com/dhcgn/picture-frame-control/internal/link"
	"github.com/dhcgn/picture-frame-control/internal/sdg"
	"github.com/dhcgn/picture-frame-control/internal/sdg/sdgtest"
)

// TestPairThenOpenOverRelay pairs with a fake frame through a fake grid and
// then reaches it over the relay, which is the path every frame outside the
// local network takes.
func TestPairThenOpenOverRelay(t *testing.T) {
	fake, err := sdgtest.NewGrid()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fake.Close(); err != nil {
			t.Errorf("fake grid reported: %v", err)
		}
	})

	const code = "12345678"
	want, err := fake.AddPairing(code[:len(code)-3], sdgtest.PairingDevice(code, nil), sdgtest.EchoDevice())
	if err != nil {
		t.Fatal(err)
	}
	id, err := sdg.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	o := link.Options{
		Identity: id,
		Name:     "test",
		Mode:     link.Relay,
		Servers:  []sdg.Endpoint{{Host: fake.Host(), Port: fake.Port()}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peer, err := link.Pair(ctx, code, o)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if peer != sdg.PeerID(want) {
		t.Fatalf("paired with %x, want %x", peer, want)
	}

	c, via, err := link.Open(ctx, peer, o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	if via != link.ViaRelay {
		t.Errorf("reached via %q, want %q", via, link.ViaRelay)
	}
}

func TestPairRejectsWrongCode(t *testing.T) {
	fake, err := sdgtest.NewGrid()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	if _, err := fake.AddPairing("12345", sdgtest.PairingDevice("12345678", nil), nil); err != nil {
		t.Fatal(err)
	}
	id, _ := sdg.NewIdentity()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = link.Pair(ctx, "99999999", link.Options{
		Identity: id,
		Servers:  []sdg.Endpoint{{Host: fake.Host(), Port: fake.Port()}},
	})
	if err == nil {
		t.Fatal("want an error for a code the grid does not know")
	}
}
