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
	defaultTTL            int    = 255
	defaultMTU            int    = 0
	defaultPort           int    = 9086
	defaultWANInterface   string = "ppp0"
	defaultHealthInterval int    = 30
	defaultHealthTarget   string = "2001:4860:4860::8888"
	defaultLANMode        string = "slaac"
	defaultBroker         string = BrokerCustom
	defaultProfileID      string = "default"
	defaultProfileName    string = "Default"

	BrokerHE        string = "he"
	BrokerIP4Market string = "ip4market"
	Broker6in4ru    string = "6in4ru"
	BrokerCustom    string = "custom"

	StorageFormatLegacy   string = "legacy"
	StorageFormatProfiles string = "profiles"
)

var supportedBrokers = []string{BrokerHE, BrokerIP4Market, Broker6in4ru, BrokerCustom}

type rawTunnelConfig struct {
	Enabled        *bool  `json:"enabled"`
	Broker         string `json:"broker"`
	RemoteEndpoint string `json:"remote_endpoint"`
	LocalIPv6      string `json:"local_ipv6"`
	RemoteIPv6     string `json:"remote_ipv6"`
	TTL            int    `json:"ttl"`
	MTU            int    `json:"mtu"`
}

type rawServerConfig struct {
	Port         *int   `json:"port"`
	WANInterface string `json:"wan_interface"`
	AuthToken    string `json:"auth_token"`
}

type rawConfig struct {
	Tunnel rawTunnelConfig `json:"tunnel"`
	LAN    LANConfig       `json:"lan"`
	Health HealthConfig    `json:"health"`
	Server rawServerConfig `json:"server"`
}

type rawProfileTunnelConfig struct {
	Broker         string `json:"broker"`
	RemoteEndpoint string `json:"remote_endpoint"`
	LocalIPv6      string `json:"local_ipv6"`
	RemoteIPv6     string `json:"remote_ipv6"`
	TTL            int    `json:"ttl"`
	MTU            int    `json:"mtu"`
}

type rawProfileConfig struct {
	Tunnel rawProfileTunnelConfig `json:"tunnel"`
	LAN    LANConfig              `json:"lan"`
	Health HealthConfig           `json:"health"`
}

type rawProfile struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Config rawProfileConfig `json:"config"`
}

type rawProfileDocument struct {
	TunnelEnabled   *bool           `json:"tunnel_enabled"`
	ActiveProfileID string          `json:"active_profile_id"`
	Profiles        []rawProfile    `json:"profiles"`
	Server          rawServerConfig `json:"server"`
}

type documentFormatProbe struct {
	TunnelEnabled   *bool             `json:"tunnel_enabled"`
	ActiveProfileID string            `json:"active_profile_id"`
	Profiles        []json.RawMessage `json:"profiles"`
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

// Config is the effective runtime configuration for the active profile.
type Config struct {
	Tunnel TunnelConfig `json:"tunnel"`
	LAN    LANConfig    `json:"lan"`
	Health HealthConfig `json:"health"`
	Server ServerConfig `json:"server"`
}

// ProfileConfig holds per-profile tunnel, LAN, and health settings.
type ProfileConfig struct {
	Tunnel TunnelConfig `json:"tunnel"`
	LAN    LANConfig    `json:"lan"`
	Health HealthConfig `json:"health"`
}

// Profile holds one saved broker profile.
type Profile struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Config           ProfileConfig `json:"config"`
	IsValid          bool          `json:"is_valid,omitempty"`
	ValidationErrors []string      `json:"validation_errors,omitempty"`
}

// Document is the persisted config model exposed to the UI/API.
type Document struct {
	StorageFormat   string       `json:"storage_format,omitempty"`
	TunnelEnabled   bool         `json:"tunnel_enabled"`
	ActiveProfileID string       `json:"active_profile_id"`
	Profiles        []Profile    `json:"profiles"`
	Server          ServerConfig `json:"server"`
}

// Load reads and parses a config file and returns the effective active configuration.
func Load(path string) (*Config, error) {
	document, err := LoadDocument(path)
	if err != nil {
		return nil, err
	}
	return document.ActiveConfig()
}

// LoadDocument reads and parses a config file into the document model.
func LoadDocument(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return ParseDocument(data)
}

// Parse reads either a legacy config or a profiles document and returns the active effective configuration.
func Parse(data []byte) (*Config, error) {
	document, err := ParseDocument(data)
	if err != nil {
		return nil, err
	}
	return document.ActiveConfig()
}

// ParseDocument reads a JSON document into the document model and applies defaults and migration.
func ParseDocument(data []byte) (*Document, error) {
	if isProfilesFormat(data) {
		return parseProfilesDocument(data)
	}
	legacyConfig, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
	}
	document := DocumentFromConfig(legacyConfig, StorageFormatLegacy)
	document.refreshProfileValidation()
	return document, nil
}

// ParseUpdate reads a JSON document intended for PUT /api/config and preserves startup-only fields when omitted.
func ParseUpdate(data []byte, current *Config) (*Config, error) {
	if isProfilesFormat(data) {
		currentDocument := DocumentFromConfig(current, StorageFormatProfiles)
		document, err := ParseUpdateDocument(data, currentDocument)
		if err != nil {
			return nil, err
		}
		return document.ActiveConfig()
	}
	cfg, err := parseLegacyConfig(data)
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

// ParseUpdateDocument reads a document intended for PUT /api/config.
func ParseUpdateDocument(data []byte, current *Document) (*Document, error) {
	document, err := ParseDocument(data)
	if err != nil {
		return nil, err
	}
	if current != nil && strings.TrimSpace(document.StorageFormat) == "" {
		document.StorageFormat = current.StorageFormat
	}
	return document, nil
}

// Save marshals cfg as a legacy config and writes it atomically to path.
func Save(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	clone := cfg.Clone()
	clone.ApplyDefaults()
	if err := clone.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := fileutil.WriteAtomically(path, data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// SaveDocument marshals the document and writes it atomically to path.
func SaveDocument(path string, document *Document) error {
	if document == nil {
		return fmt.Errorf("config document is required")
	}
	clone := document.Clone()
	clone.applyDefaults()
	if err := clone.Validate(); err != nil {
		return err
	}
	if clone.shouldSaveLegacy() {
		cfg, err := clone.ActiveConfig()
		if err != nil {
			return err
		}
		return Save(path, cfg)
	}
	data, err := json.MarshalIndent(buildRawProfileDocument(clone), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config document: %w", err)
	}
	if err := fileutil.WriteAtomically(path, data); err != nil {
		return fmt.Errorf("write config document: %w", err)
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

// Clone creates a deep copy of the Document.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	clone := *d
	clone.Profiles = make([]Profile, len(d.Profiles))
	for index := range d.Profiles {
		clone.Profiles[index] = d.Profiles[index].clone()
	}
	return &clone
}

func (p Profile) clone() Profile {
	clone := p
	clone.Config = p.Config.clone()
	clone.ValidationErrors = append([]string(nil), p.ValidationErrors...)
	return clone
}

func (c ProfileConfig) clone() ProfileConfig {
	clone := c
	clone.LAN.DNS = append([]string(nil), c.LAN.DNS...)
	clone.LAN.Networks = append([]NetworkConfig(nil), c.LAN.Networks...)
	return clone
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
	if c == nil {
		return []string{"config is required"}
	}
	return validationReasonsForConfig(c)
}

// DefaultDocument returns a single-profile default document.
func DefaultDocument() *Document {
	document := &Document{
		StorageFormat:   StorageFormatLegacy,
		TunnelEnabled:   false,
		ActiveProfileID: defaultProfileID,
		Profiles: []Profile{{
			ID:     defaultProfileID,
			Name:   defaultProfileName,
			Config: DefaultProfileConfig(),
		}},
		Server: ServerConfig{
			Port:         defaultPort,
			WANInterface: defaultWANInterface,
		},
	}
	document.refreshProfileValidation()
	return document
}

// DefaultProfileConfig returns a default per-profile config.
func DefaultProfileConfig() ProfileConfig {
	defaults := Defaults()
	defaults.Tunnel.Enabled = false
	return ProfileConfig{
		Tunnel: defaults.Tunnel,
		LAN:    defaults.LAN,
		Health: defaults.Health,
	}
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() *Config {
	return &Config{
		Tunnel: TunnelConfig{
			Enabled: false,
			Broker:  defaultBroker,
			TTL:     defaultTTL,
			MTU:     defaultMTU,
		},
		LAN: LANConfig{
			Enabled:  false,
			DNS:      []string{},
			Mode:     defaultLANMode,
			Networks: []NetworkConfig{},
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

// DocumentFromConfig wraps a single effective config as a one-profile document.
func DocumentFromConfig(cfg *Config, storageFormat string) *Document {
	if cfg == nil {
		return DefaultDocument()
	}
	clone := cfg.Clone()
	clone.ApplyDefaults()
	profileConfig := ProfileConfig{
		Tunnel: clone.Tunnel,
		LAN:    clone.LAN,
		Health: clone.Health,
	}
	profileConfig.Tunnel.Enabled = false
	document := &Document{
		StorageFormat:   storageFormat,
		TunnelEnabled:   clone.Tunnel.Enabled,
		ActiveProfileID: defaultProfileID,
		Profiles: []Profile{{
			ID:     defaultProfileID,
			Name:   defaultProfileName,
			Config: profileConfig,
		}},
		Server: clone.Server,
	}
	document.refreshProfileValidation()
	return document
}

// ActiveConfig returns the current active effective configuration.
func (d *Document) ActiveConfig() (*Config, error) {
	if d == nil {
		return nil, fmt.Errorf("config document is required")
	}
	activeProfile, err := d.ActiveProfile()
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Tunnel: activeProfile.Config.Tunnel,
		LAN:    activeProfile.Config.LAN,
		Health: activeProfile.Config.Health,
		Server: d.Server,
	}
	cfg.Tunnel.Enabled = d.TunnelEnabled
	cfg.ApplyDefaults()
	return cfg, nil
}

// ActiveProfile returns the current active profile.
func (d *Document) ActiveProfile() (*Profile, error) {
	if d == nil {
		return nil, fmt.Errorf("config document is required")
	}
	for index := range d.Profiles {
		if d.Profiles[index].ID == d.ActiveProfileID {
			profile := d.Profiles[index].clone()
			return &profile, nil
		}
	}
	return nil, fmt.Errorf("active profile %q not found", d.ActiveProfileID)
}

// Validate validates the config document and returns a ValidationError when it is invalid.
func (d *Document) Validate() error {
	reasons := d.ValidationReasons()
	if len(reasons) == 0 {
		return nil
	}
	return &ValidationError{Reasons: reasons}
}

// ValidationReasons returns all validation failures for the config document.
func (d *Document) ValidationReasons() []string {
	if d == nil {
		return []string{"config document is required"}
	}
	d.applyDefaults()
	d.refreshProfileValidation()
	reasons := validateServerConfig(d.Server)
	if len(d.Profiles) == 0 {
		return append(reasons, "profiles must contain at least one profile")
	}
	seenIDs := make(map[string]int, len(d.Profiles))
	seenNames := make(map[string]int, len(d.Profiles))
	activeIndex := -1
	for index := range d.Profiles {
		profile := d.Profiles[index]
		if strings.TrimSpace(profile.ID) == "" {
			reasons = append(reasons, fmt.Sprintf("profiles[%d].id is required", index))
		} else if previousIndex, exists := seenIDs[profile.ID]; exists {
			reasons = append(reasons, fmt.Sprintf("profiles[%d].id must be unique", index))
			reasons = append(reasons, fmt.Sprintf("profiles[%d].id conflicts with profiles[%d].id", index, previousIndex))
		} else {
			seenIDs[profile.ID] = index
		}
		if strings.TrimSpace(profile.Name) == "" {
			reasons = append(reasons, fmt.Sprintf("profiles[%d].name is required", index))
		} else if previousIndex, exists := seenNames[profile.Name]; exists {
			reasons = append(reasons, fmt.Sprintf("profiles[%d].name must be unique", index))
			reasons = append(reasons, fmt.Sprintf("profiles[%d].name conflicts with profiles[%d].name", index, previousIndex))
		} else {
			seenNames[profile.Name] = index
		}
		if profile.ID == d.ActiveProfileID {
			activeIndex = index
		}
	}
	if strings.TrimSpace(d.ActiveProfileID) == "" {
		reasons = append(reasons, "active_profile_id is required")
	} else if activeIndex == -1 {
		reasons = append(reasons, "active_profile_id must reference an existing profile")
	}
	if activeIndex >= 0 {
		for _, reason := range d.Profiles[activeIndex].ValidationErrors {
			reasons = append(reasons, fmt.Sprintf("profiles[%d].config.%s", activeIndex, reason))
		}
	}
	return reasons
}

func (c *Config) applyDefaults() {
	c.Tunnel.Broker = normalizeBroker(c.Tunnel.Broker)
	if c.Tunnel.TTL == 0 {
		c.Tunnel.TTL = defaultTTL
	}
	if c.Tunnel.MTU == 0 {
		c.Tunnel.MTU = defaultMTU
	}
	if strings.TrimSpace(c.LAN.Mode) == "" {
		c.LAN.Mode = defaultLANMode
	}
	if c.LAN.DNS == nil {
		c.LAN.DNS = []string{}
	}
	if c.LAN.Networks == nil {
		c.LAN.Networks = []NetworkConfig{}
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

func (d *Document) applyDefaults() {
	if strings.TrimSpace(d.StorageFormat) == "" {
		d.StorageFormat = StorageFormatProfiles
	}
	if strings.TrimSpace(d.ActiveProfileID) == "" {
		d.ActiveProfileID = defaultProfileID
	}
	if len(d.Profiles) == 0 {
		d.Profiles = []Profile{{
			ID:     defaultProfileID,
			Name:   defaultProfileName,
			Config: DefaultProfileConfig(),
		}}
	}
	for index := range d.Profiles {
		d.Profiles[index].Config.applyDefaults()
	}
	if d.Server.Port == 0 {
		d.Server.Port = defaultPort
	}
	if strings.TrimSpace(d.Server.WANInterface) == "" {
		d.Server.WANInterface = defaultWANInterface
	}
}

func (c *ProfileConfig) applyDefaults() {
	c.Tunnel.Broker = normalizeBroker(c.Tunnel.Broker)
	if c.Tunnel.TTL == 0 {
		c.Tunnel.TTL = defaultTTL
	}
	if c.Tunnel.MTU == 0 {
		c.Tunnel.MTU = defaultMTU
	}
	if strings.TrimSpace(c.LAN.Mode) == "" {
		c.LAN.Mode = defaultLANMode
	}
	if c.LAN.DNS == nil {
		c.LAN.DNS = []string{}
	}
	if c.LAN.Networks == nil {
		c.LAN.Networks = []NetworkConfig{}
	}
	if c.Health.IntervalSec == 0 {
		c.Health.IntervalSec = defaultHealthInterval
	}
	if strings.TrimSpace(c.Health.Target) == "" {
		c.Health.Target = defaultHealthTarget
	}
}

func (d *Document) refreshProfileValidation() {
	for index := range d.Profiles {
		tunnelEnabled := true
		if d.Profiles[index].ID == d.ActiveProfileID {
			tunnelEnabled = d.TunnelEnabled
		}
		reasons := validateProfileConfig(d.Profiles[index].Config, tunnelEnabled)
		d.Profiles[index].ValidationErrors = append([]string(nil), reasons...)
		d.Profiles[index].IsValid = len(reasons) == 0
	}
}

func (d *Document) shouldSaveLegacy() bool {
	if d.StorageFormat != StorageFormatLegacy {
		return false
	}
	if len(d.Profiles) != 1 {
		return false
	}
	profile := d.Profiles[0]
	return d.ActiveProfileID == defaultProfileID && profile.ID == defaultProfileID && profile.Name == defaultProfileName
}

func parseLegacyConfig(data []byte) (*Config, error) {
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

func parseProfilesDocument(data []byte) (*Document, error) {
	document := DefaultDocument()
	document.StorageFormat = StorageFormatProfiles
	var raw rawProfileDocument
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config document: %w", err)
	}
	if raw.TunnelEnabled != nil {
		document.TunnelEnabled = *raw.TunnelEnabled
	}
	if strings.TrimSpace(raw.ActiveProfileID) != "" {
		document.ActiveProfileID = raw.ActiveProfileID
	}
	document.Server = ServerConfig{
		WANInterface: raw.Server.WANInterface,
		AuthToken:    raw.Server.AuthToken,
	}
	if raw.Server.Port != nil {
		document.Server.Port = *raw.Server.Port
	}
	document.Profiles = make([]Profile, 0, len(raw.Profiles))
	for _, rawProfile := range raw.Profiles {
		profile := Profile{
			ID:   rawProfile.ID,
			Name: rawProfile.Name,
			Config: ProfileConfig{
				Tunnel: TunnelConfig{
					Broker:         rawProfile.Config.Tunnel.Broker,
					RemoteEndpoint: rawProfile.Config.Tunnel.RemoteEndpoint,
					LocalIPv6:      rawProfile.Config.Tunnel.LocalIPv6,
					RemoteIPv6:     rawProfile.Config.Tunnel.RemoteIPv6,
					TTL:            rawProfile.Config.Tunnel.TTL,
					MTU:            rawProfile.Config.Tunnel.MTU,
				},
				LAN:    rawProfile.Config.LAN,
				Health: rawProfile.Config.Health,
			},
		}
		document.Profiles = append(document.Profiles, profile)
	}
	document.applyDefaults()
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return document, nil
}

func buildRawProfileDocument(document *Document) rawProfileDocument {
	rawDocument := rawProfileDocument{
		TunnelEnabled:   boolPointer(document.TunnelEnabled),
		ActiveProfileID: document.ActiveProfileID,
		Profiles:        make([]rawProfile, 0, len(document.Profiles)),
		Server: rawServerConfig{
			Port:         intPointer(document.Server.Port),
			WANInterface: document.Server.WANInterface,
			AuthToken:    document.Server.AuthToken,
		},
	}
	for _, profile := range document.Profiles {
		rawDocument.Profiles = append(rawDocument.Profiles, rawProfile{
			ID:   profile.ID,
			Name: profile.Name,
			Config: rawProfileConfig{
				Tunnel: rawProfileTunnelConfig{
					Broker:         profile.Config.Tunnel.Broker,
					RemoteEndpoint: profile.Config.Tunnel.RemoteEndpoint,
					LocalIPv6:      profile.Config.Tunnel.LocalIPv6,
					RemoteIPv6:     profile.Config.Tunnel.RemoteIPv6,
					TTL:            profile.Config.Tunnel.TTL,
					MTU:            profile.Config.Tunnel.MTU,
				},
				LAN:    profile.Config.LAN,
				Health: profile.Config.Health,
			},
		})
	}
	return rawDocument
}

func validationReasonsForConfig(cfg *Config) []string {
	reasons := validateServerConfig(cfg.Server)
	profileReasons := validateProfileConfig(ProfileConfig{
		Tunnel: cfg.Tunnel,
		LAN:    cfg.LAN,
		Health: cfg.Health,
	}, cfg.Tunnel.Enabled)
	return append(reasons, profileReasons...)
}

func validateServerConfig(server ServerConfig) []string {
	reasons := make([]string, 0)
	if server.Port <= 0 {
		reasons = append(reasons, "server.port must be greater than zero")
	}
	if strings.TrimSpace(server.WANInterface) == "" {
		reasons = append(reasons, "server.wan_interface is required")
	}
	return reasons
}

func validateProfileConfig(cfg ProfileConfig, tunnelEnabled bool) []string {
	cfg.applyDefaults()
	reasons := make([]string, 0)
	if !isSupportedBroker(cfg.Tunnel.Broker) {
		reasons = append(reasons, fmt.Sprintf("tunnel.broker must be one of: %s", strings.Join(supportedBrokers, ", ")))
	}
	if cfg.Tunnel.TTL <= 0 {
		reasons = append(reasons, "tunnel.ttl must be greater than zero")
	}
	if cfg.Tunnel.MTU < 0 {
		reasons = append(reasons, "tunnel.mtu must be zero (auto) or greater than zero")
	}
	if cfg.Health.Enabled {
		if cfg.Health.IntervalSec <= 0 {
			reasons = append(reasons, "health.interval_sec must be greater than zero")
		}
		if strings.TrimSpace(cfg.Health.Target) == "" {
			reasons = append(reasons, "health.target is required when health checks are enabled")
		} else if !isValidIPv6Address(cfg.Health.Target) {
			reasons = append(reasons, "health.target must be a valid IPv6 address")
		}
	}
	if cfg.LAN.Enabled {
		if len(cfg.LAN.Networks) == 0 {
			reasons = append(reasons, "lan.networks must contain at least one network when lan.enabled=true")
		}
		for index, network := range cfg.LAN.Networks {
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
		for index, dnsServer := range cfg.LAN.DNS {
			if !isValidIPv6Address(dnsServer) {
				reasons = append(reasons, fmt.Sprintf("lan.dns[%d] must be a valid IPv6 address", index))
			}
		}
	}
	if !tunnelEnabled {
		return reasons
	}
	if strings.TrimSpace(cfg.Tunnel.RemoteEndpoint) == "" {
		reasons = append(reasons, "tunnel.remote_endpoint is required when tunnel_enabled=true")
	} else if net.ParseIP(cfg.Tunnel.RemoteEndpoint) == nil || strings.Contains(cfg.Tunnel.RemoteEndpoint, ":") {
		reasons = append(reasons, "tunnel.remote_endpoint must be a valid IPv4 address")
	}
	if strings.TrimSpace(cfg.Tunnel.LocalIPv6) == "" {
		reasons = append(reasons, "tunnel.local_ipv6 is required when tunnel_enabled=true")
	} else if !isValidIPv6Prefix(cfg.Tunnel.LocalIPv6) {
		reasons = append(reasons, "tunnel.local_ipv6 must be a valid IPv6 prefix")
	}
	return reasons
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

func isProfilesFormat(data []byte) bool {
	var probe documentFormatProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.TunnelEnabled != nil || strings.TrimSpace(probe.ActiveProfileID) != "" || probe.Profiles != nil
}

func normalizeBroker(broker string) string {
	if strings.TrimSpace(broker) == "" {
		return defaultBroker
	}
	return broker
}

func isSupportedBroker(broker string) bool {
	for _, supportedBroker := range supportedBrokers {
		if broker == supportedBroker {
			return true
		}
	}
	return false
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

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
