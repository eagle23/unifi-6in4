package ra_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
)

func TestBuildRA(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8:1::/64")
	dns := []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")}
	msg := ra.BuildRA(prefix, dns)
	if msg == nil {
		t.Fatal("BuildRA() returned nil")
	}
}

func TestAdvertiserNewAndStop(t *testing.T) {
	cfg := ra.AdvertiserConfig{
		Networks: []ra.NetworkEntry{
			{Interface: "lo", Prefix: netip.MustParsePrefix("2001:db8:1::/64")},
		},
		DNS:      []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")},
		Interval: 100 * time.Millisecond,
	}
	adv, err := ra.NewAdvertiser(cfg)
	if err != nil {
		t.Skipf("NewAdvertiser() error (expected on non-Linux): %v", err)
	}
	adv.Stop()
}
