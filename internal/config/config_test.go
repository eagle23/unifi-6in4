package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
)

func TestLoadLegacyDocumentKeepsLegacyStorageFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{
		"tunnel": {"enabled":true,"remote_endpoint":"216.66.88.98","local_ipv6":"2001:470:1f0e:abc::2/64","remote_ipv6":"2001:470:1f0e:abc::1/64","ttl":255,"mtu":1480},
		"lan": {"enabled":true,"dns":["2606:4700:4700::1111"],"mode":"slaac","networks":[{"interface":"br0","prefix":"2001:470:1f0f:1::/64","comment":"Default"}]},
		"health": {"enabled":true,"interval_sec":30,"target":"2001:4860:4860::8888","auto_restart":true},
		"server": {"port":8686,"wan_interface":"ppp0","auth_token":"test-token"}
	}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := config.LoadDocument(path)
	if err != nil {
		t.Fatalf("LoadDocument() error: %v", err)
	}
	if document.StorageFormat != config.StorageFormatLegacy {
		t.Fatalf("StorageFormat = %q, want %q", document.StorageFormat, config.StorageFormatLegacy)
	}
	if document.ActiveProfileID != "default" {
		t.Fatalf("ActiveProfileID = %q, want default", document.ActiveProfileID)
	}
	if !document.TunnelEnabled {
		t.Fatal("TunnelEnabled = false, want true")
	}
	if len(document.Profiles) != 1 {
		t.Fatalf("len(Profiles) = %d, want 1", len(document.Profiles))
	}
	profile := document.Profiles[0]
	if profile.ID != "default" {
		t.Fatalf("Profile.ID = %q, want default", profile.ID)
	}
	if profile.Name != "Default" {
		t.Fatalf("Profile.Name = %q, want Default", profile.Name)
	}
	if profile.Config.Tunnel.Broker != "custom" {
		t.Fatalf("Broker = %q, want custom", profile.Config.Tunnel.Broker)
	}
	if !profile.IsValid {
		t.Fatal("Profile.IsValid = false, want true")
	}
	if len(profile.ValidationErrors) != 0 {
		t.Fatalf("ValidationErrors = %v, want empty", profile.ValidationErrors)
	}
	effective, err := document.ActiveConfig()
	if err != nil {
		t.Fatalf("ActiveConfig() error: %v", err)
	}
	if !effective.Tunnel.Enabled {
		t.Fatal("effective.Tunnel.Enabled = false, want true")
	}
	if effective.Server.AuthToken != "test-token" {
		t.Fatalf("effective.Server.AuthToken = %q, want test-token", effective.Server.AuthToken)
	}
}

func TestSaveDocumentPreservesLegacyFormatWhenStillCompatible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatLegacy
	document.TunnelEnabled = true
	document.Server.Port = 8686
	document.Server.AuthToken = "test-token"
	document.Profiles[0].Config.Tunnel.Broker = "he"
	document.Profiles[0].Config.Tunnel.RemoteEndpoint = "216.66.88.98"
	document.Profiles[0].Config.Tunnel.LocalIPv6 = "2001:470:1f0e:abc::2/64"
	document.Profiles[0].Config.Tunnel.RemoteIPv6 = "2001:470:1f0e:abc::1/64"
	document.Profiles[0].Config.LAN.Enabled = true
	document.Profiles[0].Config.LAN.Networks = []config.NetworkConfig{{
		Interface: "br0",
		Prefix:    "2001:470:1f0f:1::/64",
		Comment:   "Default",
	}}
	if err := config.SaveDocument(path, document); err != nil {
		t.Fatalf("SaveDocument() error: %v", err)
	}
	rawData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	raw := string(rawData)
	if strings.Contains(raw, "\"profiles\"") {
		t.Fatalf("saved legacy config unexpectedly contains profiles: %s", raw)
	}
	if !strings.Contains(raw, "\"tunnel\"") {
		t.Fatalf("saved legacy config missing tunnel section: %s", raw)
	}
	loaded, err := config.LoadDocument(path)
	if err != nil {
		t.Fatalf("LoadDocument() error: %v", err)
	}
	if loaded.StorageFormat != config.StorageFormatLegacy {
		t.Fatalf("StorageFormat = %q, want %q", loaded.StorageFormat, config.StorageFormatLegacy)
	}
}

func TestSaveDocumentMigratesToProfilesFormatAfterProfileSpecificChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatLegacy
	document.TunnelEnabled = true
	document.Profiles[0].Config.Tunnel.Broker = "he"
	document.Profiles[0].Config.Tunnel.RemoteEndpoint = "216.66.88.98"
	document.Profiles[0].Config.Tunnel.LocalIPv6 = "2001:470:1f0e:abc::2/64"
	document.Profiles[0].Config.Tunnel.RemoteIPv6 = "2001:470:1f0e:abc::1/64"
	document.Profiles = append(document.Profiles, config.Profile{
		ID:   "backup",
		Name: "Backup",
		Config: config.ProfileConfig{
			Tunnel: config.TunnelConfig{
				Broker:         "he",
				RemoteEndpoint: "203.0.113.1",
				LocalIPv6:      "2001:db8::2/64",
				RemoteIPv6:     "2001:db8::1/64",
				TTL:            255,
				MTU:            0,
			},
			LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
			Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
		},
	})
	if err := config.SaveDocument(path, document); err != nil {
		t.Fatalf("SaveDocument() error: %v", err)
	}
	rawData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	raw := string(rawData)
	if !strings.Contains(raw, "\"profiles\"") {
		t.Fatalf("saved profiles config missing profiles section: %s", raw)
	}
	if strings.Contains(raw, "\"storage_format\"") {
		t.Fatalf("saved profiles config must not persist storage_format: %s", raw)
	}
	loaded, err := config.LoadDocument(path)
	if err != nil {
		t.Fatalf("LoadDocument() error: %v", err)
	}
	if loaded.StorageFormat != config.StorageFormatProfiles {
		t.Fatalf("StorageFormat = %q, want %q", loaded.StorageFormat, config.StorageFormatProfiles)
	}
	if len(loaded.Profiles) != 2 {
		t.Fatalf("len(Profiles) = %d, want 2", len(loaded.Profiles))
	}
}

func TestDocumentValidateRejectsDuplicateNamesAndInvalidActiveProfile(t *testing.T) {
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatProfiles
	document.ActiveProfileID = "missing"
	document.Profiles = append(document.Profiles, config.Profile{
		ID:   "second",
		Name: "Default",
		Config: config.ProfileConfig{
			Tunnel: config.TunnelConfig{Broker: "broken"},
			LAN:    config.LANConfig{DNS: []string{}, Networks: []config.NetworkConfig{}},
			Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
		},
	})
	err := document.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want validation error")
	}
	validationErr, ok := err.(*config.ValidationError)
	if !ok {
		t.Fatalf("Validate() error type = %T, want *config.ValidationError", err)
	}
	actualReasons := strings.Join(validationErr.Reasons, "\n")
	expectedParts := []string{
		"active_profile_id must reference an existing profile",
		"profiles[1].name must be unique",
	}
	for _, expectedPart := range expectedParts {
		if !strings.Contains(actualReasons, expectedPart) {
			t.Fatalf("validation reasons missing %q in %q", expectedPart, actualReasons)
		}
	}
	if document.Profiles[1].IsValid {
		t.Fatal("inactive invalid profile unexpectedly marked valid")
	}
	if len(document.Profiles[1].ValidationErrors) == 0 {
		t.Fatal("inactive invalid profile validation errors = 0, want > 0")
	}
}

func TestDocumentValidateAllowsInvalidInactiveDraftProfiles(t *testing.T) {
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatProfiles
	document.Profiles = append(document.Profiles, config.Profile{
		ID:   "draft",
		Name: "Draft",
		Config: config.ProfileConfig{
			Tunnel: config.TunnelConfig{Broker: "custom", TTL: 255, MTU: 0},
			LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
			Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
		},
	})
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if len(document.Profiles) != 2 {
		t.Fatalf("len(Profiles) = %d, want 2", len(document.Profiles))
	}
	if document.Profiles[1].IsValid {
		t.Fatal("inactive draft unexpectedly marked valid")
	}
	if len(document.Profiles[1].ValidationErrors) == 0 {
		t.Fatal("inactive draft validation errors = 0, want > 0")
	}
}

func TestLoadConfigStillReturnsEffectiveLegacyCompatibleConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{
		"tunnel_enabled": true,
		"active_profile_id": "he-home",
		"profiles": [
			{
				"id": "he-home",
				"name": "HE Home",
				"config": {
					"tunnel": {"broker":"he","remote_endpoint":"216.66.88.98","local_ipv6":"2001:470:1f0e:abc::2/64","remote_ipv6":"2001:470:1f0e:abc::1/64","ttl":255,"mtu":1480},
					"lan": {"enabled":true,"dns":["2606:4700:4700::1111"],"mode":"slaac","networks":[{"interface":"br0","prefix":"2001:470:1f0f:1::/64","comment":"Default"}]},
					"health": {"enabled":true,"interval_sec":30,"target":"2001:4860:4860::8888","auto_restart":true}
				}
			}
		],
		"server": {"port":8686,"wan_interface":"ppp0","auth_token":"test-token"}
	}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Tunnel.Broker != "he" {
		t.Errorf("Broker = %q, want %q", cfg.Tunnel.Broker, "he")
	}
	if !cfg.Tunnel.Enabled {
		t.Error("Tunnel.Enabled = false, want true")
	}
	if cfg.Server.AuthToken != "test-token" {
		t.Errorf("AuthToken = %q, want %q", cfg.Server.AuthToken, "test-token")
	}
}

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Tunnel.TTL != 255 {
		t.Errorf("TTL = %d, want 255", cfg.Tunnel.TTL)
	}
	if cfg.Tunnel.MTU != 0 {
		t.Errorf("MTU = %d, want 0", cfg.Tunnel.MTU)
	}
	if cfg.Server.Port != 9086 {
		t.Errorf("Port = %d, want 9086", cfg.Server.Port)
	}
	if cfg.Server.WANInterface != "ppp0" {
		t.Errorf("WANInterface = %q, want %q", cfg.Server.WANInterface, "ppp0")
	}
	if cfg.Health.IntervalSec != 30 {
		t.Errorf("IntervalSec = %d, want 30", cfg.Health.IntervalSec)
	}
	if cfg.LAN.Mode != "slaac" {
		t.Errorf("Mode = %q, want %q", cfg.LAN.Mode, "slaac")
	}
	if len(cfg.LAN.DNS) != 0 {
		t.Errorf("len(DNS) = %d, want 0", len(cfg.LAN.DNS))
	}
}
