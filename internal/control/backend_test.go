package control

import (
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

func TestProbeUsesRouteSelectedGlobalSource(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 route get 2001:4860:4860::8888": []byte("2001:4860:4860::8888 from :: dev sit-6in4 src 2a0b:4140:3d89:100::1 metric 1024 pref medium\n"),
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
			"ip -6 route get 2001:4860:4860::8888": []byte("2001:4860:4860::8888 from :: dev sit-6in4 src fd42:4242:4242:1::2 metric 1024 pref medium\n"),
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

func TestEnsureLANInterfaceReadyDisablesDADAndBouncesProblematicInterface(t *testing.T) {
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
	if !containsCommand(runner.calls, "ip link set dev br0 down") {
		t.Fatalf("expected br0 down bounce, calls = %v", runner.calls)
	}
	if !containsCommand(runner.calls, "ip link set dev br0 up") {
		t.Fatalf("expected br0 up bounce, calls = %v", runner.calls)
	}
}

func TestEnsureLANInterfaceReadySkipsBounceWhenLinkLocalIsHealthy(t *testing.T) {
	runner := &fakeCommandRunner{
		outputs: map[string][]byte{
			"ip -6 addr show dev br0": []byte("34: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    inet6 fe80::aa9c:6cff:fe82:1a45/64 scope link\n"),
		},
	}
	backend := NewSystemBackendWithRunner(runner)
	if err := backend.ensureLANInterfaceReady("br0"); err != nil {
		t.Fatalf("ensureLANInterfaceReady() error: %v", err)
	}
	if containsCommand(runner.calls, "ip link set dev br0 down") || containsCommand(runner.calls, "ip link set dev br0 up") {
		t.Fatalf("did not expect interface bounce, calls = %v", runner.calls)
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
}

func containsCommand(calls []string, expected string) bool {
	for _, call := range calls {
		if call == expected {
			return true
		}
	}
	return false
}
