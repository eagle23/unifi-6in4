package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{
		"tunnel": {"broker":"he","remote_endpoint":"216.66.88.98","local_ipv6":"2001:470:1f0e:abc::2/64","remote_ipv6":"2001:470:1f0e:abc::1/64","ttl":255,"mtu":1480},
		"lan": {"enabled":true,"dns":["2606:4700:4700::1111"],"mode":"slaac","networks":[{"interface":"br0","prefix":"2001:470:1f0f:1::/64","comment":"Default"}]},
		"health": {"enabled":true,"interval_sec":30,"target":"2001:4860:4860::8888","auto_restart":true},
		"server": {"port":8686,"wan_interface":"ppp0","auth_token":"test-token"}
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Tunnel.Broker != "he" {
		t.Errorf("Broker = %q, want %q", cfg.Tunnel.Broker, "he")
	}
	if cfg.Tunnel.RemoteEndpoint != "216.66.88.98" {
		t.Errorf("RemoteEndpoint = %q, want %q", cfg.Tunnel.RemoteEndpoint, "216.66.88.98")
	}
	if cfg.Tunnel.MTU != 1480 {
		t.Errorf("MTU = %d, want %d", cfg.Tunnel.MTU, 1480)
	}
	if !cfg.LAN.Enabled {
		t.Error("LAN.Enabled = false, want true")
	}
	if len(cfg.LAN.Networks) != 1 {
		t.Fatalf("len(Networks) = %d, want 1", len(cfg.LAN.Networks))
	}
	if cfg.LAN.Networks[0].Interface != "br0" {
		t.Errorf("Networks[0].Interface = %q, want %q", cfg.LAN.Networks[0].Interface, "br0")
	}
	if cfg.Server.Port != 8686 {
		t.Errorf("Port = %d, want %d", cfg.Server.Port, 8686)
	}
	if cfg.Server.AuthToken != "test-token" {
		t.Errorf("AuthToken = %q, want %q", cfg.Server.AuthToken, "test-token")
	}
}

func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &config.Config{
		Tunnel: config.TunnelConfig{Broker: "ip4market", RemoteEndpoint: "1.2.3.4", LocalIPv6: "2001:db8::2/64", RemoteIPv6: "2001:db8::1/64", TTL: 255, MTU: 1480},
		LAN:    config.LANConfig{Enabled: true, DNS: []string{"2606:4700:4700::1111"}, Mode: "slaac", Networks: []config.NetworkConfig{{Interface: "br0", Prefix: "2001:db8:1::/64", Comment: "Default"}}},
		Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
		Server: config.ServerConfig{Port: 8686, WANInterface: "ppp0", AuthToken: "secret"},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if loaded.Tunnel.Broker != "ip4market" {
		t.Errorf("Broker = %q, want %q", loaded.Tunnel.Broker, "ip4market")
	}
	if loaded.Server.AuthToken != "secret" {
		t.Errorf("AuthToken = %q, want %q", loaded.Server.AuthToken, "secret")
	}
}

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Tunnel.TTL != 255 {
		t.Errorf("TTL = %d, want 255", cfg.Tunnel.TTL)
	}
	if cfg.Tunnel.MTU != 1480 {
		t.Errorf("MTU = %d, want 1480", cfg.Tunnel.MTU)
	}
	if cfg.Server.Port != 8686 {
		t.Errorf("Port = %d, want 8686", cfg.Server.Port)
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
}
