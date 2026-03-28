package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/eagle23/unifi-tunnel-4to6/internal/fileutil"
)

const (
	defaultTTL            int = 255
	defaultMTU            int = 1480
	defaultPort           int = 8686
	defaultWANInterface   string = "ppp0"
	defaultHealthInterval int = 30
	defaultHealthTarget   string = "2001:4860:4860::8888"
	defaultLANMode        string = "slaac"
)

var defaultDNSServers = []string{"2606:4700:4700::1111", "2001:4860:4860::8888"}

type rawTunnelConfig struct {
	Enabled        *bool  `json:"enabled"`
	Broker         string `json:"broker"`
	RemoteEndpoint string `json:"remote_endpoint"`
	LocalIPv6      string `json:"local_ipv6"`
	RemoteIPv6     string `json:"remote_ipv6"`
	TTL            int    `json:"ttl"`
	MTU            int    `json:"mtu"`
}

type rawConfig struct {
	Tunnel rawTunnelConfig `json:"tunnel"`
	Server struct {
		Port *int `json:"port"`
	} `json:"server"`
}

// ValidationError represents one or more configuration validation failures.
type ValidationError struct {
	Reasons []string
}

// Error returns the validation error as a single message.
func (e *ValidationError) Error() string {
	return strings.Join(e.Reasons, "; ")
}

// TunnelConfig holds 6in4 tunnel parameters.
type TunnelConfig struct {
	Enabled        bool   `json:"enabled"`
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
	return Parse(data)
}

// Parse reads a JSON document into a Config and applies defaults and migration.
func Parse(data []byte) (*Config, error) {
	cfg := Defaults()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.applyLegacyTunnelEnabled(data); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

// ParseUpdate reads a JSON document intended for PUT /api/config and preserves startup-only fields when omitted.
func ParseUpdate(data []byte, current *Config) (*Config, error) {
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return cfg, nil
	}
	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config for update migration: %w", err)
	}
	if raw.Tunnel.Enabled == nil {
		cfg.Tunnel.Enabled = current.Tunnel.Enabled
	}
	if raw.Server.Port == nil {
		cfg.Server.Port = current.Server.Port
	}
	return cfg, nil
}

// Save marshals cfg as indented JSON and writes it atomically to path.
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := fileutil.WriteAtomically(path, data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Clone creates a deep copy of the Config.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	clone := *c
	clone.LAN.DNS = append([]string(nil), c.LAN.DNS...)
	clone.LAN.Networks = append([]NetworkConfig(nil), c.LAN.Networks...)
	return &clone
}

// ApplyDefaults fills empty fields with default values.
func (c *Config) ApplyDefaults() {
	c.applyDefaults()
}

// Validate validates the config and returns a ValidationError when it is invalid.
func (c *Config) Validate() error {
	reasons := c.ValidationReasons()
	if len(reasons) == 0 {
		return nil
	}
	return &ValidationError{Reasons: reasons}
}

// ValidationReasons returns all validation failures for the config.
func (c *Config) ValidationReasons() []string {
	reasons := make([]string, 0)
	if c.Server.Port <= 0 {
		reasons = append(reasons, "server.port must be greater than zero")
	}
	if strings.TrimSpace(c.Server.WANInterface) == "" {
		reasons = append(reasons, "server.wan_interface is required")
	}
	if c.Tunnel.TTL <= 0 {
		reasons = append(reasons, "tunnel.ttl must be greater than zero")
	}
	if c.Tunnel.MTU <= 0 {
		reasons = append(reasons, "tunnel.mtu must be greater than zero")
	}
	if c.Health.Enabled {
		if c.Health.IntervalSec <= 0 {
			reasons = append(reasons, "health.interval_sec must be greater than zero")
		}
		if strings.TrimSpace(c.Health.Target) == "" {
			reasons = append(reasons, "health.target is required when health checks are enabled")
		} else if !isValidIPv6Address(c.Health.Target) {
			reasons = append(reasons, "health.target must be a valid IPv6 address")
		}
	}
	if c.LAN.Enabled {
		if len(c.LAN.Networks) == 0 {
			reasons = append(reasons, "lan.networks must contain at least one network when lan.enabled=true")
		}
		for index, network := range c.LAN.Networks {
			if strings.TrimSpace(network.Interface) == "" {
				reasons = append(reasons, fmt.Sprintf("lan.networks[%d].interface is required", index))
			}
			if strings.TrimSpace(network.Prefix) == "" {
				reasons = append(reasons, fmt.Sprintf("lan.networks[%d].prefix is required", index))
				continue
			}
			prefix, err := netip.ParsePrefix(network.Prefix)
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("lan.networks[%d].prefix must be a valid IPv6 prefix", index))
				continue
			}
			if !prefix.Addr().Is6() {
				reasons = append(reasons, fmt.Sprintf("lan.networks[%d].prefix must be IPv6", index))
			}
		}
		for index, dnsServer := range c.LAN.DNS {
			if !isValidIPv6Address(dnsServer) {
				reasons = append(reasons, fmt.Sprintf("lan.dns[%d] must be a valid IPv6 address", index))
			}
		}
	}
	if !c.Tunnel.Enabled {
		return reasons
	}
	if strings.TrimSpace(c.Tunnel.RemoteEndpoint) == "" {
		reasons = append(reasons, "tunnel.remote_endpoint is required when tunnel.enabled=true")
	} else if net.ParseIP(c.Tunnel.RemoteEndpoint) == nil || strings.Contains(c.Tunnel.RemoteEndpoint, ":") {
		reasons = append(reasons, "tunnel.remote_endpoint must be a valid IPv4 address")
	}
	if strings.TrimSpace(c.Tunnel.LocalIPv6) == "" {
		reasons = append(reasons, "tunnel.local_ipv6 is required when tunnel.enabled=true")
	} else if !isValidIPv6Prefix(c.Tunnel.LocalIPv6) {
		reasons = append(reasons, "tunnel.local_ipv6 must be a valid IPv6 prefix")
	}
	return reasons
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() *Config {
	return &Config{
		Tunnel: TunnelConfig{
			Enabled: false,
			TTL:     defaultTTL,
			MTU:     defaultMTU,
		},
		LAN: LANConfig{
			Enabled: false,
			DNS:     append([]string(nil), defaultDNSServers...),
			Mode:    defaultLANMode,
		},
		Health: HealthConfig{
			Enabled:     true,
			IntervalSec: defaultHealthInterval,
			Target:      defaultHealthTarget,
			AutoRestart: true,
		},
		Server: ServerConfig{
			Port:         defaultPort,
			WANInterface: defaultWANInterface,
		},
	}
}

func (c *Config) applyDefaults() {
	if c.Tunnel.TTL == 0 {
		c.Tunnel.TTL = defaultTTL
	}
	if c.Tunnel.MTU == 0 {
		c.Tunnel.MTU = defaultMTU
	}
	if len(c.LAN.DNS) == 0 {
		c.LAN.DNS = append([]string(nil), defaultDNSServers...)
	}
	if strings.TrimSpace(c.LAN.Mode) == "" {
		c.LAN.Mode = defaultLANMode
	}
	if c.Health.IntervalSec == 0 {
		c.Health.IntervalSec = defaultHealthInterval
	}
	if strings.TrimSpace(c.Health.Target) == "" {
		c.Health.Target = defaultHealthTarget
	}
	if c.Server.Port == 0 {
		c.Server.Port = defaultPort
	}
	if strings.TrimSpace(c.Server.WANInterface) == "" {
		c.Server.WANInterface = defaultWANInterface
	}
}

func (c *Config) applyLegacyTunnelEnabled(data []byte) error {
	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config for migration: %w", err)
	}
	if raw.Tunnel.Enabled != nil {
		c.Tunnel.Enabled = *raw.Tunnel.Enabled
		return nil
	}
	c.Tunnel.Enabled = strings.TrimSpace(c.Tunnel.RemoteEndpoint) != "" && strings.TrimSpace(c.Tunnel.LocalIPv6) != ""
	return nil
}

func isValidIPv6Address(value string) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return address.Is6()
}

func isValidIPv6Prefix(value string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return prefix.Addr().Is6()
}

