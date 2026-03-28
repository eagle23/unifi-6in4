package control

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

// ServiceParams holds construction parameters for the control service.
type ServiceParams struct {
	ConfigPath string
	StatePath  string
	Backend    Backend
}

// Service is the single control-plane brain for desired and observed state.
type Service struct {
	mu         sync.Mutex
	configPath string
	statePath  string
	backend    Backend
	config     *config.Config
	state      *StateDocument
	advertiser *ra.Advertiser
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

// NewService creates the control service and loads persisted config/state.
func NewService(params ServiceParams) (*Service, error) {
	if params.ConfigPath == "" {
		return nil, fmt.Errorf("config path is required")
	}
	if params.StatePath == "" {
		return nil, fmt.Errorf("state path is required")
	}
	backend := params.Backend
	if backend == nil {
		backend = NewSystemBackend()
	}
	cfg, err := loadOrCreateConfig(params.ConfigPath)
	if err != nil {
		return nil, err
	}
	state, err := LoadState(params.StatePath)
	if err != nil {
		return nil, err
	}
	return &Service{
		configPath: params.ConfigPath,
		statePath:  params.StatePath,
		backend:    backend,
		config:     cfg,
		state:      state,
		stopCh:     make(chan struct{}),
	}, nil
}

// Start performs the initial reconcile and launches background health checks.
func (s *Service) Start() error {
	if err := s.reconcile(false); err != nil {
		return err
	}
	s.wg.Add(1)
	go s.healthLoop()
	return nil
}

// Stop shuts down background workers and the active RA advertiser.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.mu.Lock()
	s.stopAdvertiserLocked()
	s.mu.Unlock()
	s.wg.Wait()
}

// GetConfig returns the current desired config.
func (s *Service) GetConfig() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Clone()
}

// CurrentToken returns the current auth token.
func (s *Service) CurrentToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Server.AuthToken
}

// Status returns the last observed state snapshot.
func (s *Service) Status() (*tunnel.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.CloneStatus(), nil
}

// Health performs a fresh probe and returns the updated status.
func (s *Service) Health() (*tunnel.Status, error) {
	return s.runHealthCheck(false)
}

// UpdateConfig saves a new desired config and reconciles it immediately.
func (s *Service) UpdateConfig(cfg *config.Config) error {
	updated := cfg.Clone()
	s.mu.Lock()
	current := s.config.Clone()
	s.mu.Unlock()
	preserveStartupFields(updated, current)
	updated.ApplyDefaults()
	if err := updated.Validate(); err != nil {
		return err
	}
	if err := config.Save(s.configPath, updated); err != nil {
		return err
	}
	s.mu.Lock()
	s.config = updated
	s.mu.Unlock()
	return s.reconcile(false)
}

// Up enables the desired tunnel state and reconciles it.
func (s *Service) Up() error {
	return s.mutateConfig(func(cfg *config.Config) {
		cfg.Tunnel.Enabled = true
	})
}

// Down disables the desired tunnel state and reconciles it.
func (s *Service) Down() error {
	return s.mutateConfig(func(cfg *config.Config) {
		cfg.Tunnel.Enabled = false
	})
}

// Restart forces a full reconcile without changing desired state.
func (s *Service) Restart() error {
	return s.reconcile(true)
}

func (s *Service) mutateConfig(mutate func(cfg *config.Config)) error {
	s.mu.Lock()
	nextConfig := s.config.Clone()
	s.mu.Unlock()
	mutate(nextConfig)
	nextConfig.ApplyDefaults()
	if err := nextConfig.Validate(); err != nil {
		return err
	}
	if err := config.Save(s.configPath, nextConfig); err != nil {
		return err
	}
	s.mu.Lock()
	s.config = nextConfig
	s.mu.Unlock()
	return s.reconcile(false)
}

func (s *Service) healthLoop() {
	defer s.wg.Done()
	for {
		interval := s.currentHealthInterval()
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-s.stopCh:
			timer.Stop()
			return
		}
		_, _ = s.runHealthCheck(true)
	}
}

func (s *Service) runHealthCheck(allowAutoRestart bool) (*tunnel.Status, error) {
	s.mu.Lock()
	cfg := s.config.Clone()
	s.mu.Unlock()
	if !cfg.Health.Enabled || cfg.Health.Target == "" {
		return s.Status()
	}
	result, err := s.backend.Probe(cfg.Health.Target)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.state.PingOK = result.PingOK
	s.state.PingMs = result.PingMs
	if result.PingOK {
		s.removeDegradedReasonLocked(tunnel.ReasonHealthProbeFailed)
	} else if !containsString(s.state.DegradedReasons, tunnel.ReasonHealthProbeFailed) && cfg.Tunnel.Enabled {
		s.state.DegradedReasons = append(s.state.DegradedReasons, tunnel.ReasonHealthProbeFailed)
	}
	if err := SaveState(s.statePath, s.state); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	status := s.state.CloneStatus()
	s.mu.Unlock()
	if allowAutoRestart && cfg.Health.AutoRestart && cfg.Tunnel.Enabled {
		if cfg.Validate() == nil && !result.PingOK {
			if err := s.reconcile(true); err != nil {
				return nil, err
			}
			return s.Status()
		}
	}
	return status, nil
}

func (s *Service) reconcile(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.config.Clone()
	validationErr := cfg.Validate()
	validationReasons := validationReasons(validationErr)
	previousApplied := s.state.Applied
	s.state.Interface = tunnel.InterfaceName
	s.state.DesiredEnabled = cfg.Tunnel.Enabled
	s.state.ConfigValid = len(validationReasons) == 0
	s.state.ReconcileState = tunnel.ReconcileReconciling
	s.stopAdvertiserLocked()
	backendErr := s.backend.Reconcile(ReconcileInput{
		Config:      cfg,
		ConfigValid: len(validationReasons) == 0,
		Force:       force,
		Applied:     previousApplied,
	})
	advertiserErr := error(nil)
	if backendErr == nil && len(validationReasons) == 0 && cfg.Tunnel.Enabled && cfg.LAN.Enabled && len(cfg.LAN.Networks) > 0 {
		advertiserErr = s.startAdvertiserLocked(cfg)
	}
	observation, observeErr := s.backend.Observe(ObserveInput{WANInterface: cfg.Server.WANInterface})
	probeResult := ProbeResult{}
	if len(validationReasons) == 0 && cfg.Tunnel.Enabled && cfg.Health.Enabled && cfg.Health.Target != "" {
		probeResult, _ = s.backend.Probe(cfg.Health.Target)
	}
	s.state.WANIPv4 = observation.WANIPv4
	s.state.LocalIPv6 = cfg.Tunnel.LocalIPv6
	s.state.Networks = buildAppliedState(cfg).Networks
	s.state.TunnelUp = observation.TunnelUp
	s.state.PingOK = probeResult.PingOK
	s.state.PingMs = probeResult.PingMs
	s.state.DegradedReasons = validationReasons
	appendErrorReason(&s.state.DegradedReasons, backendErr)
	appendErrorReason(&s.state.DegradedReasons, advertiserErr)
	appendErrorReason(&s.state.DegradedReasons, observeErr)
	if cfg.Tunnel.Enabled && cfg.Health.Enabled && !probeResult.PingOK {
		if !containsString(s.state.DegradedReasons, tunnel.ReasonHealthProbeFailed) {
			s.state.DegradedReasons = append(s.state.DegradedReasons, tunnel.ReasonHealthProbeFailed)
		}
	}
	s.state.LastError = firstReason(s.state.DegradedReasons)
	s.state.LastReconcileAt = time.Now().UTC()
	switch {
	case !cfg.Tunnel.Enabled:
		s.state.ReconcileState = tunnel.ReconcileDown
		s.state.Applied = emptyAppliedState()
	case len(validationReasons) > 0:
		s.state.ReconcileState = tunnel.ReconcileDegraded
	case backendErr != nil || advertiserErr != nil || observeErr != nil:
		s.state.ReconcileState = tunnel.ReconcileDegraded
		s.state.Applied = buildAppliedState(cfg)
	case observation.TunnelUp:
		s.state.ReconcileState = tunnel.ReconcileReady
		s.state.Applied = buildAppliedState(cfg)
	default:
		s.state.ReconcileState = tunnel.ReconcileDegraded
		s.state.Applied = buildAppliedState(cfg)
	}
	if !cfg.LAN.Enabled {
		s.state.Networks = []tunnel.NetworkStatus{}
	}
	if cfg.Health.Enabled && !cfg.Tunnel.Enabled {
		s.state.PingOK = false
		s.state.PingMs = 0
	}
	if err := SaveState(s.statePath, s.state); err != nil {
		return err
	}
	if len(validationReasons) > 0 {
		return nil
	}
	if backendErr != nil {
		return backendErr
	}
	if advertiserErr != nil {
		return advertiserErr
	}
	if observeErr != nil {
		return observeErr
	}
	return nil
}

func (s *Service) startAdvertiserLocked(cfg *config.Config) error {
	networks := make([]ra.NetworkEntry, 0, len(cfg.LAN.Networks))
	for _, network := range cfg.LAN.Networks {
		prefix, err := netip.ParsePrefix(network.Prefix)
		if err != nil {
			return fmt.Errorf("parse RA prefix %q: %w", network.Prefix, err)
		}
		networks = append(networks, ra.NetworkEntry{
			Interface: network.Interface,
			Prefix:    prefix,
		})
	}
	dnsServers := make([]netip.Addr, 0, len(cfg.LAN.DNS))
	for _, dnsValue := range cfg.LAN.DNS {
		dnsServer, err := netip.ParseAddr(dnsValue)
		if err != nil {
			return fmt.Errorf("parse RA dns %q: %w", dnsValue, err)
		}
		dnsServers = append(dnsServers, dnsServer)
	}
	advertiser, err := ra.NewAdvertiser(ra.AdvertiserConfig{
		Networks: networks,
		DNS:      dnsServers,
		Interval: 10 * time.Second,
	})
	if err != nil {
		return err
	}
	s.advertiser = advertiser
	return nil
}

func (s *Service) stopAdvertiserLocked() {
	if s.advertiser == nil {
		return
	}
	s.advertiser.Stop()
	s.advertiser = nil
}

func (s *Service) removeDegradedReasonLocked(reason string) {
	filtered := make([]string, 0, len(s.state.DegradedReasons))
	for _, currentReason := range s.state.DegradedReasons {
		if currentReason == reason {
			continue
		}
		filtered = append(filtered, currentReason)
	}
	s.state.DegradedReasons = filtered
}

func (s *Service) currentHealthInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	interval := time.Duration(s.config.Health.IntervalSec) * time.Second
	if interval < 5*time.Second {
		return 30 * time.Second
	}
	return interval
}

func emptyAppliedState() AppliedState {
	return AppliedState{Networks: []tunnel.NetworkStatus{}}
}

func appendErrorReason(reasons *[]string, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if !containsString(*reasons, message) {
		*reasons = append(*reasons, message)
	}
}

func firstReason(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return reasons[0]
}

func validationReasons(err error) []string {
	if err == nil {
		return []string{}
	}
	var validationErr *config.ValidationError
	if errors.As(err, &validationErr) {
		return append([]string(nil), validationErr.Reasons...)
	}
	return []string{err.Error()}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func preserveStartupFields(nextConfig *config.Config, currentConfig *config.Config) {
	if nextConfig.Server.Port == 0 {
		nextConfig.Server.Port = currentConfig.Server.Port
	}
}

func loadOrCreateConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	cfg = config.Defaults()
	if err := config.Save(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
