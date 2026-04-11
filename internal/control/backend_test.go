package control

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
)

type fakeCommandRunner struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []string
}

func (f *fakeCommandRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	commandLine := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, commandLine)
	if err, ok := f.errors[commandLine]; ok {
		return []byte{}, err
	}
	if output, ok := f.outputs[commandLine]; ok {
		return output, nil
	}
	return []byte{}, nil
}

func TestResolveTunnelMTUUsesConfiguredValue(t *testing.T) {
	backend := NewSystemBackendWithRunner(&fakeCommandRunner{})
	actualMTU, err := backend.resolveTunnelMTU(1472, "ppp0")
	if err != nil {
		t.Fatalf("resolveTunnelMTU() error: %v", err)
	}
	if actualMTU != 1472 {
		t.Fatalf("resolveTunnelMTU() = %d, want 1472", actualMTU)
	}
}

func TestResolveTunnelMTUAutoFromWANInterface(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip link show ppp0": []byte("40: ppp0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1492 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 1000\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	actualMTU, err := backend.resolveTunnelMTU(0, "ppp0")
	if err != nil {
		t.Fatalf("resolveTunnelMTU() error: %v", err)
	}
	if actualMTU != 1472 {
		t.Fatalf("resolveTunnelMTU() = %d, want 1472", actualMTU)
	}
}

func TestReconcileAutoMTUUsesResolvedValue(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -4 addr show ppp0": []byte("40: ppp0    inet 78.36.199.233/32 scope global ppp0\n"),
			"ip link show ppp0":    []byte("40: ppp0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1492 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 1000\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "216.66.80.90"
	cfg.Tunnel.LocalIPv6 = "2001:470:27:103d::2/64"
	cfg.Tunnel.MTU = 0
	input := ReconcileInput{
		Config:      cfg,
		ConfigValid: true,
	}
	if err := backend.Reconcile(input); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
	if cfg.Tunnel.MTU != 1472 {
		t.Fatalf("cfg.Tunnel.MTU = %d, want 1472", cfg.Tunnel.MTU)
	}
	if !containsCommand(runner.calls, "ip link set sit-6in4 mtu 1472") {
		t.Fatalf("expected mtu command with 1472, calls = %v", runner.calls)
	}
}

func TestReconcileUsesGlobalLANGatewayAsDefaultRouteSource(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -4 addr show ppp0":    []byte("40: ppp0    inet 78.36.199.233/32 scope global ppp0\n"),
			"ip link show ppp0":       []byte("40: ppp0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1492 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 1000\n"),
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "89.22.239.139"
	cfg.Tunnel.LocalIPv6 = "fd42:4242:4242:1::2/64"
	cfg.Tunnel.MTU = 0
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{
			Interface: "br0",
			Prefix:    "2a0b:4140:3d89:100::/64",
			Comment:   "Default LAN",
		},
	}
	input := ReconcileInput{
		Config:      cfg,
		ConfigValid: true,
	}
	if err := backend.Reconcile(input); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
	if !containsCommand(runner.calls, "ip -6 route replace ::/0 dev sit-6in4 src 2a0b:4140:3d89:100::1") {
		t.Fatalf("expected default route with global source, calls = %v", runner.calls)
	}
}

func TestReconcilePrefersGlobalTunnelAddressAsDefaultRouteSource(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -4 addr show ppp0":    []byte("40: ppp0    inet 78.36.199.233/32 scope global ppp0\n"),
			"ip link show ppp0":       []byte("40: ppp0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1492 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 1000\n"),
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "89.22.239.139"
	cfg.Tunnel.LocalIPv6 = "2a11:6c7:f31:81::2/64"
	cfg.Tunnel.MTU = 0
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{
			Interface: "br0",
			Prefix:    "2a11:6c7:1800:8100::/64",
			Comment:   "Default LAN",
		},
	}
	input := ReconcileInput{
		Config:      cfg,
		ConfigValid: true,
	}
	if err := backend.Reconcile(input); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
	if !containsCommand(runner.calls, "ip -6 route replace ::/0 dev sit-6in4 src 2a11:6c7:f31:81::2") {
		t.Fatalf("expected default route to prefer tunnel source, calls = %v", runner.calls)
	}
	if containsCommand(runner.calls, "ip -6 route replace ::/0 dev sit-6in4 src 2a11:6c7:1800:8100::1") {
		t.Fatalf("did not expect LAN gateway source, calls = %v", runner.calls)
	}
}

func TestReconcileSkipsExistingLANGatewayAddress(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -4 addr show ppp0": []byte("40: ppp0    inet 78.36.199.233/32 scope global ppp0\n"),
			"ip link show ppp0":    []byte("40: ppp0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1492 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 1000\n"),
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n" +
				"    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link\n" +
				"    inet6 2a11:6c7:1800:8100::1/64 scope global\n"),
		},
		errors: map[string]error{
			"ip -6 addr add 2a11:6c7:1800:8100::1/64 dev br0": fmt.Errorf("exit status 2"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "89.22.239.139"
	cfg.Tunnel.LocalIPv6 = "2a11:6c7:1800:8000::2/64"
	cfg.Tunnel.MTU = 0
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{
			Interface: "br0",
			Prefix:    "2a11:6c7:1800:8100::/64",
			Comment:   "Default LAN",
		},
	}
	input := ReconcileInput{
		Config:      cfg,
		ConfigValid: true,
		Applied:     emptyAppliedState(),
	}
	if err := backend.Reconcile(input); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
	if containsCommand(runner.calls, "ip -6 addr add 2a11:6c7:1800:8100::1/64 dev br0") {
		t.Fatalf("did not expect duplicate LAN gateway add, calls = %v", runner.calls)
	}
}

func TestObserveReportsMissingManagedIPv6Addresses(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip link show sit-6in4": []byte("50: sit-6in4@NONE: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1472\n"),
			"ip -4 addr show ppp0":  []byte("40: ppp0    inet 78.36.199.233/32 scope global ppp0\n"),
			"ip -6 addr show dev sit-6in4": []byte("50: sit-6in4@NONE: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1472\n" +
				"    inet6 2a11:6c7:1800:8000::2/64 scope global\n"),
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n" +
				"    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "89.22.239.139"
	cfg.Tunnel.LocalIPv6 = "2a11:6c7:1800:8000::2/64"
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{
			Interface: "br0",
			Prefix:    "2a11:6c7:1800:8100::/64",
			Comment:   "Default LAN",
		},
	}
	observation, err := backend.Observe(ObserveInput{WANInterface: "ppp0", Config: cfg})
	if err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	expectedMissingAddress := "br0 missing 2a11:6c7:1800:8100::1/64"
	if !containsString(observation.MissingIPv6Addresses, expectedMissingAddress) {
		t.Fatalf("MissingIPv6Addresses = %v, want %q", observation.MissingIPv6Addresses, expectedMissingAddress)
	}
}

func TestProbeUsesRouteSelectedGlobalSource(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 route get 2001:4860:4860::8888":                               []byte("2001:4860:4860::8888 from :: dev sit-6in4 src 2a0b:4140:3d89:100::1 metric 1024 pref medium\n"),
			"ping -6 -n -c 1 -W 3 -I 2a0b:4140:3d89:100::1 2001:4860:4860::8888": []byte("64 bytes from 2001:4860:4860::8888: icmp_seq=1 ttl=118 time=72.5 ms\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	actualResult, err := backend.Probe("2001:4860:4860::8888")
	if err != nil {
		t.Fatalf("Probe() error: %v", err)
	}
	if !actualResult.PingOK {
		t.Fatal("Probe() PingOK = false, want true")
	}
	if actualResult.PingMs != 72 {
		t.Fatalf("Probe() PingMs = %d, want 72", actualResult.PingMs)
	}
	if !containsCommand(runner.calls, "ping -6 -n -c 1 -W 3 -I 2a0b:4140:3d89:100::1 2001:4860:4860::8888") {
		t.Fatalf("expected source-bound ping, calls = %v", runner.calls)
	}
}

func TestProbeFallsBackToPlainPingWithoutGlobalRouteSource(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 route get 2001:4860:4860::8888":      []byte("2001:4860:4860::8888 from :: dev sit-6in4 src fd42:4242:4242:1::2 metric 1024 pref medium\n"),
			"ping -6 -n -c 1 -W 3 2001:4860:4860::8888": []byte("64 bytes from 2001:4860:4860::8888: icmp_seq=1 ttl=118 time=80.1 ms\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	actualResult, err := backend.Probe("2001:4860:4860::8888")
	if err != nil {
		t.Fatalf("Probe() error: %v", err)
	}
	if !actualResult.PingOK {
		t.Fatal("Probe() PingOK = false, want true")
	}
	if !containsCommand(runner.calls, "ping -6 -n -c 1 -W 3 2001:4860:4860::8888") {
		t.Fatalf("expected plain ping fallback, calls = %v", runner.calls)
	}
}

func TestEnsureLANInterfaceReadyTogglesDisableIPv6ForProblematicLinkLocal(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link dadfailed tentative\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	originalSleepFn := sleepFn
	sleepFn = func(duration time.Duration) {}
	defer func() {
		sleepFn = originalSleepFn
	}()
	if err := backend.ensureLANInterfaceReady("br0"); err != nil {
		t.Fatalf("ensureLANInterfaceReady() error: %v", err)
	}
	if !containsCommand(runner.calls, "sysctl -w net.ipv6.conf.br0.accept_dad=0") {
		t.Fatalf("expected accept_dad override, calls = %v", runner.calls)
	}
	if !containsCommand(runner.calls, "sysctl -w net.ipv6.conf.br0.disable_ipv6=1") {
		t.Fatalf("expected disable_ipv6=1 toggle, calls = %v", runner.calls)
	}
	if !containsCommand(runner.calls, "sysctl -w net.ipv6.conf.br0.disable_ipv6=0") {
		t.Fatalf("expected disable_ipv6=0 toggle, calls = %v", runner.calls)
	}
	if containsCommand(runner.calls, "ip link set dev br0 down") || containsCommand(runner.calls, "ip link set dev br0 up") {
		t.Fatalf("did not expect L2 interface bounce, calls = %v", runner.calls)
	}
}

func TestEnsureLANInterfaceReadySkipsToggleWhenLinkLocalIsHealthy(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	if err := backend.ensureLANInterfaceReady("br0"); err != nil {
		t.Fatalf("ensureLANInterfaceReady() error: %v", err)
	}
	if containsCommand(runner.calls, "sysctl -w net.ipv6.conf.br0.disable_ipv6=1") {
		t.Fatalf("did not expect disable_ipv6 toggle, calls = %v", runner.calls)
	}
}

func TestHasProblematicLinkLocalDetectsOnlyBrokenLinkLocalAddresses(t *testing.T) {
	if !hasProblematicLinkLocal("inet6 fe80::1/64 scope link dadfailed tentative") {
		t.Fatal("expected dadfailed tentative link-local to be problematic")
	}
	if !hasProblematicLinkLocal("inet6 fe80::1/64 scope link tentative") {
		t.Fatal("expected tentative link-local to be problematic")
	}
	if hasProblematicLinkLocal("inet6 fe80::1/64 scope link") {
		t.Fatal("expected healthy link-local to be accepted")
	}
	if hasProblematicLinkLocal("inet6 2001:470::1/64 scope global dadfailed tentative") {
		t.Fatal("expected non-link-local addresses to be ignored")
	}
	// Multi-line: healthy link-local alongside a tentative global must NOT flag the interface.
	healthyOutput := "inet6 fe80::1/64 scope link\n    valid_lft forever preferred_lft forever\ninet6 2001:470::1/64 scope global tentative"
	if hasProblematicLinkLocal(healthyOutput) {
		t.Fatal("expected healthy link-local with tentative global to not be problematic")
	}
	// Multi-line: broken link-local below a healthy global — must flag.
	brokenOutput := "inet6 2001:470::1/64 scope global\n    valid_lft forever preferred_lft forever\ninet6 fe80::1/64 scope link dadfailed"
	if !hasProblematicLinkLocal(brokenOutput) {
		t.Fatal("expected dadfailed link-local to be detected in multi-line output")
	}
}

func TestRepairAddressesAddsMissingWithoutBounce(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev sit-6in4": []byte("50: sit-6in4: <UP> mtu 1472\n"),
			"ip -6 addr show dev br0":      []byte("34: br0: <UP> mtu 1500\n    inet6 fe80::1/64 scope link\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{Interface: "br0", Prefix: "2001:470:1f0e::/64"},
	}
	if err := backend.RepairAddresses(RepairInput{Config: cfg}); err != nil {
		t.Fatalf("RepairAddresses() error: %v", err)
	}
	if !containsCommand(runner.calls, "ip -6 addr add 2001:470::2/64 dev sit-6in4") {
		t.Fatalf("expected tunnel addr add, calls = %v", runner.calls)
	}
	if !containsCommand(runner.calls, "ip -6 addr add 2001:470:1f0e::1/64 dev br0") {
		t.Fatalf("expected gateway addr add on br0, calls = %v", runner.calls)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "ip tunnel") || strings.Contains(call, "iptables") || strings.Contains(call, "link set") {
			t.Fatalf("repair touched heavy-path command %q (calls=%v)", call, runner.calls)
		}
	}
}

func TestRepairAddressesSkipsWhenAddressesPresent(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev sit-6in4": []byte("50: sit-6in4: <UP> mtu 1472\n    inet6 2001:470::2/64 scope global\n"),
			"ip -6 addr show dev br0":      []byte("34: br0: <UP> mtu 1500\n    inet6 fe80::1/64 scope link\n    inet6 2001:470:1f0e::1/64 scope global\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{Interface: "br0", Prefix: "2001:470:1f0e::/64"},
	}
	if err := backend.RepairAddresses(RepairInput{Config: cfg}); err != nil {
		t.Fatalf("RepairAddresses() error: %v", err)
	}
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "ip -6 addr add") {
			t.Fatalf("unexpected addr add when already present, calls = %v", runner.calls)
		}
	}
}

func TestRepairAddressesReplacesDadfailedAddress(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev sit-6in4": []byte("50: sit-6in4: <UP> mtu 1472\n    inet6 2001:470::2/64 scope global\n"),
			"ip -6 addr show dev br0":      []byte("34: br0: <UP> mtu 1500\n    inet6 fe80::1/64 scope link\n    inet6 2001:470:1f0e::1/64 scope global dadfailed tentative\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{Interface: "br0", Prefix: "2001:470:1f0e::/64"},
	}
	if err := backend.RepairAddresses(RepairInput{Config: cfg}); err != nil {
		t.Fatalf("RepairAddresses() error: %v", err)
	}
	if !containsCommand(runner.calls, "ip -6 addr del 2001:470:1f0e::1/64 dev br0") {
		t.Fatalf("expected dadfailed addr del on br0, calls = %v", runner.calls)
	}
	if !containsCommand(runner.calls, "ip -6 addr add 2001:470:1f0e::1/64 dev br0") {
		t.Fatalf("expected fresh addr add on br0, calls = %v", runner.calls)
	}
	// Sit addr is healthy → must NOT be deleted.
	if containsCommand(runner.calls, "ip -6 addr del 2001:470::2/64 dev sit-6in4") {
		t.Fatalf("healthy sit addr should not be deleted, calls = %v", runner.calls)
	}
}

func TestCollectMissingManagedIPv6AddressesReportsDadfailed(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev sit-6in4": []byte("50: sit-6in4: <UP> mtu 1472\n    inet6 2001:470::2/64 scope global\n"),
			"ip -6 addr show dev br0":      []byte("34: br0: <UP> mtu 1500\n    inet6 2001:470:1f0e::1/64 scope global dadfailed\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.LAN.Enabled = true
	cfg.LAN.Networks = []config.NetworkConfig{
		{Interface: "br0", Prefix: "2001:470:1f0e::/64"},
	}
	missing, err := backend.collectMissingManagedIPv6Addresses(cfg)
	if err != nil {
		t.Fatalf("collectMissingManagedIPv6Addresses() error: %v", err)
	}
	expected := "br0 unhealthy 2001:470:1f0e::1/64"
	found := false
	for _, entry := range missing {
		if entry == expected {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing entries = %v, want %q", missing, expected)
	}
}

func TestFindIPv6PrefixStateDistinguishesHealthAndFlags(t *testing.T) {
	parsePrefix := func(value string) netip.Prefix {
		p, err := netip.ParsePrefix(value)
		if err != nil {
			t.Fatalf("ParsePrefix(%q) error: %v", value, err)
		}
		return p
	}
	desired := parsePrefix("2001:470:1f0e::1/64")
	healthy := "    inet6 2001:470:1f0e::1/64 scope global\n       valid_lft forever preferred_lft forever\n"
	if got := findIPv6PrefixState(healthy, desired); got != ipv6AddressHealthy {
		t.Fatalf("healthy: got %v, want ipv6AddressHealthy", got)
	}
	dadfailed := "    inet6 2001:470:1f0e::1/64 scope global dadfailed tentative\n"
	if got := findIPv6PrefixState(dadfailed, desired); got != ipv6AddressUnhealthy {
		t.Fatalf("dadfailed: got %v, want ipv6AddressUnhealthy", got)
	}
	tentative := "    inet6 2001:470:1f0e::1/64 scope global tentative\n"
	if got := findIPv6PrefixState(tentative, desired); got != ipv6AddressUnhealthy {
		t.Fatalf("tentative: got %v, want ipv6AddressUnhealthy", got)
	}
	missing := "    inet6 fe80::1/64 scope link\n"
	if got := findIPv6PrefixState(missing, desired); got != ipv6AddressMissing {
		t.Fatalf("missing: got %v, want ipv6AddressMissing", got)
	}
}

func containsCommand(calls []string, expected string) bool {
	for _, call := range calls {
		if call == expected {
			return true
		}
	}
	return false
}
