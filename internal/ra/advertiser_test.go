package ra_test

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
	"github.com/mdlayher/ndp"
)

func TestBuildRA(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8:1::/64")
	dns := []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")}
	iface := &net.Interface{
		Name:         "eth0",
		MTU:          1500,
		HardwareAddr: net.HardwareAddr{0x02, 0x42, 0xac, 0x11, 0x00, 0x02},
	}
	msg := ra.BuildRA(iface, prefix, dns, 1472)
	if msg == nil {
		t.Fatal("BuildRA() returned nil")
	}
	if msg.RouterLifetime != 30*time.Minute {
		t.Fatalf("RouterLifetime = %s, want %s", msg.RouterLifetime, 30*time.Minute)
	}
	if len(msg.Options) != 4 {
		t.Fatalf("len(Options) = %d, want 4", len(msg.Options))
	}
	if _, ok := msg.Options[0].(*ndp.LinkLayerAddress); !ok {
		t.Fatalf("Options[0] type = %T, want *ndp.LinkLayerAddress", msg.Options[0])
	}
	if _, ok := msg.Options[1].(*ndp.MTU); !ok {
		t.Fatalf("Options[1] type = %T, want *ndp.MTU", msg.Options[1])
	}
	mtuOption, ok := msg.Options[1].(*ndp.MTU)
	if !ok {
		t.Fatalf("Options[1] type = %T, want *ndp.MTU", msg.Options[1])
	}
	if mtuOption.MTU != 1472 {
		t.Fatalf("Options[1].MTU = %d, want 1472", mtuOption.MTU)
	}
	if _, ok := msg.Options[2].(*ndp.PrefixInformation); !ok {
		t.Fatalf("Options[2] type = %T, want *ndp.PrefixInformation", msg.Options[2])
	}
	if _, ok := msg.Options[3].(*ndp.RecursiveDNSServer); !ok {
		t.Fatalf("Options[3] type = %T, want *ndp.RecursiveDNSServer", msg.Options[3])
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
