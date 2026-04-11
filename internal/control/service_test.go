package control

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
)

type fakeBackend struct {
	reconcileCalls []ReconcileInput
	repairCalls    []RepairInput
	observation    Observation
	probeResult    ProbeResult
	reconcileErr   error
	reconcileErrs  []error
	observeErr     error
	probeErr       error
	repairErr      error
}

func (f *fakeBackend) Reconcile(input ReconcileInput) error {
	f.reconcileCalls = append(f.reconcileCalls, input)
	callIndex := len(f.reconcileCalls) - 1
	if callIndex < len(f.reconcileErrs) {
		return f.reconcileErrs[callIndex]
	}
	return f.reconcileErr
}

func (f *fakeBackend) Observe(input ObserveInput) (*Observation, error) {
	observation := f.observation
	return &observation, f.observeErr
}

func (f *fakeBackend) Probe(target string) (ProbeResult, error) {
	return f.probeResult, f.probeErr
}

func (f *fakeBackend) RepairAddresses(input RepairInput) error {
	f.repairCalls = append(f.repairCalls, input)
	if f.repairErr == nil {
		f.observation.MissingIPv6Addresses = nil
	}
	return f.repairErr
}

func TestServiceStartWithBlankConfigKeepsDataplaneDown(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	backend := &fakeBackend{observation: Observation{TunnelUp: false}}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	status, err := service.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.DesiredEnabled {
		t.Error("DesiredEnabled = true, want false")
	}
	if status.ReconcileState != "down" {
		t.Errorf("ReconcileState = %q, want %q", status.ReconcileState, "down")
	}
	if status.TunnelUp {
		t.Error("TunnelUp = true, want false")
	}
}

func TestServiceUpRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	if err := config.Save(configPath, config.Defaults()); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    &fakeBackend{},
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	if err := service.Up(); err == nil {
		t.Fatal("Up() error = nil, want validation error")
	}
	if service.GetConfig().Tunnel.Enabled {
		t.Error("Tunnel.Enabled = true, want false after failed validation")
	}
}

func TestServiceValidConfigTransitionsToReady(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	cfg := config.Defaults()
	cfg.Tunnel.RemoteEndpoint = "216.66.88.98"
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.Tunnel.Enabled = false
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	backend := &fakeBackend{
		observation: Observation{TunnelUp: true, WANIPv4: "78.36.199.233"},
		probeResult: ProbeResult{PingOK: true, PingMs: 42},
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if err := service.Up(); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	status, err := service.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if !status.DesiredEnabled {
		t.Error("DesiredEnabled = false, want true")
	}
	if status.ReconcileState != "ready" {
		t.Errorf("ReconcileState = %q, want %q", status.ReconcileState, "ready")
	}
	if !status.PingOK {
		t.Error("PingOK = false, want true")
	}
	if status.LastPingAt.IsZero() {
		t.Error("LastPingAt is zero, want probe timestamp")
	}
	if len(backend.reconcileCalls) == 0 {
		t.Fatal("expected at least one reconcile call")
	}
	if !backend.reconcileCalls[len(backend.reconcileCalls)-1].Config.Tunnel.Enabled {
		t.Error("last reconcile did not enable tunnel")
	}
}

func TestServiceMarksMissingManagedIPv6AddressDegraded(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "216.66.88.98"
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.Health.Enabled = false
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	backend := &fakeBackend{
		observation: Observation{
			TunnelUp:             true,
			WANIPv4:              "78.36.199.233",
			MissingIPv6Addresses: []string{"br0 missing 2001:470:1f0e:abc::1/64"},
		},
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	status, err := service.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	expectedReason := "managed IPv6 address missing: br0 missing 2001:470:1f0e:abc::1/64"
	if status.ReconcileState != "degraded" {
		t.Fatalf("ReconcileState = %q, want degraded", status.ReconcileState)
	}
	if !containsString(status.DegradedReasons, expectedReason) {
		t.Fatalf("DegradedReasons = %v, want %q", status.DegradedReasons, expectedReason)
	}
}

func TestServiceHealthRepairsMissingManagedIPv6AddressWhenProbeOK(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "216.66.88.98"
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.Health.Enabled = true
	cfg.Health.AutoRestart = true
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	backend := &fakeBackend{
		observation: Observation{
			TunnelUp:             true,
			WANIPv4:              "78.36.199.233",
			MissingIPv6Addresses: []string{"sit-6in4 missing 2001:470::2/64"},
		},
		probeResult: ProbeResult{PingOK: true, PingMs: 42},
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	service.state.ReconcileState = "ready"
	baselineReconcile := len(backend.reconcileCalls)
	baselineRepair := len(backend.repairCalls)
	if _, err := service.runHealthCheck(true); err != nil {
		t.Fatalf("runHealthCheck() error: %v", err)
	}
	if gotDelta := len(backend.reconcileCalls) - baselineReconcile; gotDelta != 0 {
		t.Fatalf("reconcileCalls delta = %d, want 0", gotDelta)
	}
	if gotDelta := len(backend.repairCalls) - baselineRepair; gotDelta != 1 {
		t.Fatalf("repairCalls delta = %d, want 1", gotDelta)
	}
	if backend.repairCalls[len(backend.repairCalls)-1].Config == nil {
		t.Fatal("repair call missing Config")
	}
}

func TestServiceHealthFallsBackToReconcileWhenRepairFails(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "216.66.88.98"
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.Health.Enabled = true
	cfg.Health.AutoRestart = true
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	backend := &fakeBackend{
		observation: Observation{
			TunnelUp:             true,
			WANIPv4:              "78.36.199.233",
			MissingIPv6Addresses: []string{"sit-6in4 missing 2001:470::2/64"},
		},
		probeResult: ProbeResult{PingOK: true, PingMs: 42},
		repairErr:   errors.New("repair boom"),
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	service.state.ReconcileState = "ready"
	baselineReconcile := len(backend.reconcileCalls)
	baselineRepair := len(backend.repairCalls)
	if _, err := service.runHealthCheck(true); err != nil {
		t.Fatalf("runHealthCheck() error: %v", err)
	}
	if gotDelta := len(backend.repairCalls) - baselineRepair; gotDelta != 1 {
		t.Fatalf("repairCalls delta = %d, want 1", gotDelta)
	}
	if gotDelta := len(backend.reconcileCalls) - baselineReconcile; gotDelta != 1 {
		t.Fatalf("reconcileCalls delta = %d, want 1 (fallback)", gotDelta)
	}
}

func TestServiceHealthFullReconcilesWhenProbeFails(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "216.66.88.98"
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.Health.Enabled = true
	cfg.Health.AutoRestart = true
	cfg.Health.Target = "2001:4860:4860::8888"
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	backend := &fakeBackend{
		observation: Observation{
			TunnelUp: true,
			WANIPv4:  "78.36.199.233",
		},
		probeResult: ProbeResult{PingOK: false},
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	service.state.ReconcileState = "ready"
	baselineReconcile := len(backend.reconcileCalls)
	baselineRepair := len(backend.repairCalls)
	if _, err := service.runHealthCheck(true); err != nil {
		t.Fatalf("runHealthCheck() error: %v", err)
	}
	if gotDelta := len(backend.repairCalls) - baselineRepair; gotDelta != 0 {
		t.Fatalf("repairCalls delta = %d, want 0", gotDelta)
	}
	if gotDelta := len(backend.reconcileCalls) - baselineReconcile; gotDelta != 1 {
		t.Fatalf("reconcileCalls delta = %d, want 1", gotDelta)
	}
}

func TestServiceStatusIncludesActiveProfileMetadata(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatProfiles
	document.TunnelEnabled = true
	document.ActiveProfileID = "he-home"
	document.Profiles = []config.Profile{
		{
			ID:   "he-home",
			Name: "HE Home",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{
					Broker:         "he",
					RemoteEndpoint: "216.66.88.98",
					LocalIPv6:      "2001:470::2/64",
					RemoteIPv6:     "2001:470::1/64",
					TTL:            255,
					MTU:            0,
				},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
	}
	if err := config.SaveDocument(configPath, document); err != nil {
		t.Fatalf("SaveDocument() error: %v", err)
	}
	backend := &fakeBackend{
		observation: Observation{TunnelUp: true, WANIPv4: "78.36.199.233"},
		probeResult: ProbeResult{PingOK: true, PingMs: 42},
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	status, err := service.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.ActiveProfileID != "he-home" {
		t.Errorf("ActiveProfileID = %q, want %q", status.ActiveProfileID, "he-home")
	}
	if status.ActiveProfileName != "HE Home" {
		t.Errorf("ActiveProfileName = %q, want %q", status.ActiveProfileName, "HE Home")
	}
	if status.ActiveBroker != "he" {
		t.Errorf("ActiveBroker = %q, want %q", status.ActiveBroker, "he")
	}
}

func TestServiceUpdateConfigDocumentRejectsInvalidActiveSwitchAtomically(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatProfiles
	document.TunnelEnabled = true
	document.ActiveProfileID = "primary"
	document.Profiles = []config.Profile{
		{
			ID:   "primary",
			Name: "Primary",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{
					Broker:         "he",
					RemoteEndpoint: "216.66.88.98",
					LocalIPv6:      "2001:470::2/64",
					RemoteIPv6:     "2001:470::1/64",
					TTL:            255,
					MTU:            0,
				},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
		{
			ID:   "draft",
			Name: "Draft",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{Broker: "custom", TTL: 255, MTU: 0},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
	}
	if err := config.SaveDocument(configPath, document); err != nil {
		t.Fatalf("SaveDocument() error: %v", err)
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    &fakeBackend{},
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	nextDocument := document.Clone()
	nextDocument.ActiveProfileID = "draft"
	if err := service.UpdateConfigDocument(nextDocument); err == nil {
		t.Fatal("UpdateConfigDocument() error = nil, want validation error")
	}
	currentDocument := service.GetConfigDocument()
	if currentDocument.ActiveProfileID != "primary" {
		t.Errorf("ActiveProfileID = %q, want %q", currentDocument.ActiveProfileID, "primary")
	}
}

func TestServiceUpdateConfigDocumentKeepsCurrentProfileWhenReconcileFails(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatProfiles
	document.TunnelEnabled = true
	document.ActiveProfileID = "primary"
	document.Profiles = []config.Profile{
		{
			ID:   "primary",
			Name: "Primary",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{
					Broker:         "he",
					RemoteEndpoint: "216.66.88.98",
					LocalIPv6:      "2001:470::2/64",
					RemoteIPv6:     "2001:470::1/64",
					TTL:            255,
					MTU:            0,
				},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
		{
			ID:   "backup",
			Name: "Backup",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{
					Broker:         "custom",
					RemoteEndpoint: "198.51.100.10",
					LocalIPv6:      "2001:db8::2/64",
					RemoteIPv6:     "2001:db8::1/64",
					TTL:            255,
					MTU:            0,
				},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
	}
	if err := config.SaveDocument(configPath, document); err != nil {
		t.Fatalf("SaveDocument() error: %v", err)
	}
	backend := &fakeBackend{reconcileErr: errors.New("backend boom")}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	nextDocument := document.Clone()
	nextDocument.ActiveProfileID = "backup"
	err = service.UpdateConfigDocument(nextDocument)
	if err == nil {
		t.Fatal("UpdateConfigDocument() error = nil, want backend error")
	}
	currentDocument := service.GetConfigDocument()
	if currentDocument.ActiveProfileID != "primary" {
		t.Errorf("ActiveProfileID = %q, want %q", currentDocument.ActiveProfileID, "primary")
	}
	persistedDocument, loadErr := config.LoadDocument(configPath)
	if loadErr != nil {
		t.Fatalf("LoadDocument() error: %v", loadErr)
	}
	if persistedDocument.ActiveProfileID != "primary" {
		t.Errorf("persisted ActiveProfileID = %q, want %q", persistedDocument.ActiveProfileID, "primary")
	}
}

func TestServiceRetriesFailedInitialReconcileInBackground(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	statePath := dir + "/state.json"
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.RemoteEndpoint = "216.66.88.98"
	cfg.Tunnel.LocalIPv6 = "2001:470::2/64"
	cfg.Health.Enabled = false
	cfg.Health.IntervalSec = 5
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	backend := &fakeBackend{
		reconcileErrs: []error{errors.New("read WAN IPv4: device not ready"), nil},
		observation:   Observation{TunnelUp: true, WANIPv4: "78.36.199.233"},
	}
	service, err := NewService(ServiceParams{
		ConfigPath: configPath,
		StatePath:  statePath,
		Backend:    backend,
	})
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	defer service.Stop()
	if err := service.Start(); err == nil {
		t.Fatal("Start() error = nil, want initial reconcile failure")
	}
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := service.Status()
		if statusErr != nil {
			t.Fatalf("Status() error: %v", statusErr)
		}
		if status.ReconcileState == "ready" {
			if len(backend.reconcileCalls) < 2 {
				t.Fatalf("reconcileCalls = %d, want at least 2", len(backend.reconcileCalls))
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("service did not recover to ready state, last reconcile state = %q", mustStatusState(t, service))
}

func mustStatusState(t *testing.T, service *Service) string {
	t.Helper()
	status, err := service.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	return status.ReconcileState
}

func TestResolveRADNSServersUsesConfiguredValues(t *testing.T) {
	cfg := config.Defaults()
	cfg.LAN.DNS = []string{"2001:4860:4860::8888"}
	actualDNS, err := resolveRADNSServers(cfg)
	if err != nil {
		t.Fatalf("resolveRADNSServers() error: %v", err)
	}
	expectedDNS := netip.MustParseAddr("2001:4860:4860::8888")
	if len(actualDNS) != 1 || actualDNS[0] != expectedDNS {
		t.Fatalf("actualDNS = %v, want [%s]", actualDNS, expectedDNS)
	}
}

func TestResolveRADNSServersFallsBackToRouterGatewayAddresses(t *testing.T) {
	cfg := config.Defaults()
	cfg.LAN.Networks = []config.NetworkConfig{
		{Interface: "br0", Prefix: "2001:470:28:1038::/64"},
		{Interface: "br10", Prefix: "2001:470:28:1038::/64"},
		{Interface: "br20", Prefix: "2001:470:28:1039::/64"},
	}
	actualDNS, err := resolveRADNSServers(cfg)
	if err != nil {
		t.Fatalf("resolveRADNSServers() error: %v", err)
	}
	expectedDNS := []netip.Addr{
		netip.MustParseAddr("2001:470:28:1038::1"),
		netip.MustParseAddr("2001:470:28:1039::1"),
	}
	if len(actualDNS) != len(expectedDNS) {
		t.Fatalf("len(actualDNS) = %d, want %d", len(actualDNS), len(expectedDNS))
	}
	for index := range expectedDNS {
		if actualDNS[index] != expectedDNS[index] {
			t.Fatalf("actualDNS[%d] = %s, want %s", index, actualDNS[index], expectedDNS[index])
		}
	}
}
