package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// TunnelConfig holds 6in4 tunnel parameters.
type TunnelConfig struct {
	Broker         string `json:"broker"`
	RemoteEndpoint string `json:"remote_endpoint"`
	LocalIPv6      string `json:"local_ipv6"`
	RemoteIPv6     string `json:"remote_ipv6"`
	TTL            int    `json:"ttl"`
	MTU            int    `json:"mtu"`
}

// NetworkConfig describes a single LAN network to advertise.
type NetworkConfig struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
	Comment   string `json:"comment"`
}

// LANConfig holds LAN-side IPv6 advertisement settings.
type LANConfig struct {
	Enabled  bool            `json:"enabled"`
	DNS      []string        `json:"dns"`
	Mode     string          `json:"mode"`
	Networks []NetworkConfig `json:"networks"`
}

// HealthConfig holds tunnel health-check settings.
type HealthConfig struct {
	Enabled     bool   `json:"enabled"`
	IntervalSec int    `json:"interval_sec"`
	Target      string `json:"target"`
	AutoRestart bool   `json:"auto_restart"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port         int    `json:"port"`
	WANInterface string `json:"wan_interface"`
	AuthToken    string `json:"auth_token"`
}

// Config is the top-level application configuration.
type Config struct {
	Tunnel TunnelConfig `json:"tunnel"`
	LAN    LANConfig    `json:"lan"`
	Health HealthConfig `json:"health"`
	Server ServerConfig `json:"server"`
}

// Load reads and parses a JSON config file from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// Save marshals cfg as indented JSON and writes it to path.
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() *Config {
	return &Config{
		Tunnel: TunnelConfig{
			TTL: 255,
			MTU: 1480,
		},
		LAN: LANConfig{
			Enabled: false,
			DNS:     []string{"2606:4700:4700::1111", "2001:4860:4860::8888"},
			Mode:    "slaac",
		},
		Health: HealthConfig{
			Enabled:     true,
			IntervalSec: 30,
			Target:      "2001:4860:4860::8888",
			AutoRestart: true,
		},
		Server: ServerConfig{
			Port:         8686,
			WANInterface: "ppp0",
		},
	}
}
