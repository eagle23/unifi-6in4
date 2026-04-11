package control

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

const managedIPv6AddressMissingReasonPrefix = "managed IPv6 address missing: "

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
	document   *config.Document
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
	document, cfg, err := loadOrCreateConfigDocument(params.ConfigPath)
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
		document:   document,
		config:     cfg,
		state:      state,
		stopCh:     make(chan struct{}),
	}, nil
}

// Start launches background workers and performs the initial reconcile.
func (s *Service) Start() error {
	s.wg.Add(1)
	go s.healthLoop()
	if err := s.reconcile(false); err != nil {
		return err
	}
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

// GetConfigDocument returns the current desired config document.
func (s *Service) GetConfigDocument() *config.Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	document := s.document.Clone()
	_ = document.Validate()
	return document
}

// CurrentToken returns the current auth token.
func (s *Service) CurrentToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.document.Server.AuthToken
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
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	s.mu.Lock()
	nextDocument := s.document.Clone()
	s.mu.Unlock()
	nextDocument.TunnelEnabled = cfg.Tunnel.Enabled
	nextDocument.Server = cfg.Server
	if err := replaceActiveProfileConfig(nextDocument, cfg); err != nil {
		return err
	}
	return s.UpdateConfigDocument(nextDocument)
}

// UpdateConfigDocument saves a new desired config document and reconciles it immediately.
func (s *Service) UpdateConfigDocument(document *config.Document) error {
	if document == nil {
		return fmt.Errorf("config document is required")
	}
	updatedDocument := document.Clone()
	s.mu.Lock()
	defer s.mu.Unlock()
	currentDocument := s.document.Clone()
	currentConfig := s.config.Clone()
	currentState := s.state.Clone()
	preserveDocumentStartupFields(updatedDocument, currentDocument)
	if err := updatedDocument.Validate(); err != nil {
		return err
	}
	updatedConfig, err := updatedDocument.ActiveConfig()
	if err != nil {
		return err
	}
	s.document = updatedDocument
	s.config = updatedConfig
	if err := s.reconcileLocked(false); err != nil {
		return s.rollbackConfigUpdateLocked(currentDocument, currentConfig, currentState, err)
	}
	if err := config.SaveDocument(s.configPath, updatedDocument); err != nil {
		return s.rollbackConfigUpdateLocked(currentDocument, currentConfig, currentState, err)
	}
	return nil
}

// Up enables the desired tunnel state and reconciles it.
func (s *Service) Up() error {
	return s.mutateDocument(func(document *config.Document) {
		document.TunnelEnabled = true
	})
}

// Down disables the desired tunnel state and reconciles it.
func (s *Service) Down() error {
	return s.mutateDocument(func(document *config.Document) {
		document.TunnelEnabled = false
	})
}

// Restart forces a full reconcile without changing desired state.
func (s *Service) Restart() error {
	return s.reconcile(true)
}

func (s *Service) mutateDocument(mutate func(document *config.Document)) error {
	s.mu.Lock()
	nextDocument := s.document.Clone()
	s.mu.Unlock()
	mutate(nextDocument)
	return s.UpdateConfigDocument(nextDocument)
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
		if s.shouldRetryReconcile() {
			_ = s.reconcile(true)
			continue
		}
		_, _ = s.runHealthCheck(true)
	}
}

func (s *Service) shouldRetryReconcile() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.document.TunnelEnabled {
		return false
	}
	if s.state.ReconcileState == tunnel.ReconcileReady {
		return false
	}
	return s.config.Validate() == nil
}

func (s *Service) runHealthCheck(allowAutoRestart bool) (*tunnel.Status, error) {
	s.mu.Lock()
	cfg := s.config.Clone()
	s.mu.Unlock()
	validationErr := cfg.Validate()
	validationReasons := validationReasons(validationErr)
	observation, observeErr := s.backend.Observe(buildObserveInput(cfg, validationReasons))
	if observation == nil {
		observation = &Observation{}
	}
	result := ProbeResult{}
	probeTime := time.Time{}
	if cfg.Health.Enabled && cfg.Health.Target != "" {
		probeResult, err := s.backend.Probe(cfg.Health.Target)
		if err == nil {
			result = probeResult
			probeTime = time.Now().UTC()
		}
	}
	s.mu.Lock()
	if observeErr == nil && observation != nil {
		s.state.WANIPv4 = observation.WANIPv4
		s.state.TunnelUp = observation.TunnelUp
	}
	s.state.PingOK = result.PingOK
	s.state.PingMs = result.PingMs
	if !probeTime.IsZero() {
		s.state.LastPingAt = probeTime
	}
	s.replaceManagedIPv6AddressReasonsLocked(managedIPv6AddressReasons(observation))
	appendErrorReason(&s.state.DegradedReasons, observeErr)
	if cfg.Health.Enabled {
		if result.PingOK {
			s.removeDegradedReasonLocked(tunnel.ReasonHealthProbeFailed)
		} else if !containsString(s.state.DegradedReasons, tunnel.ReasonHealthProbeFailed) && cfg.Tunnel.Enabled {
			s.state.DegradedReasons = append(s.state.DegradedReasons, tunnel.ReasonHealthProbeFailed)
		}
	}
	if observeErr != nil || hasMissingManagedIPv6Addresses(observation) {
		s.state.ReconcileState = tunnel.ReconcileDegraded
	}
	s.state.LastError = firstReason(s.state.DegradedReasons)
	if err := SaveState(s.statePath, s.state); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	status := s.state.CloneStatus()
	s.mu.Unlock()
	shouldRecoverFailedProbe := cfg.Health.Enabled && !result.PingOK
	shouldRecoverMissingAddress := hasMissingManagedIPv6Addresses(observation)
	if allowAutoRestart && cfg.Health.AutoRestart && cfg.Tunnel.Enabled && len(validationReasons) == 0 {
		if shouldRecoverFailedProbe {
			slog.Warn("health probe failed, triggering full reconcile",
				"target", cfg.Health.Target,
				"ping_ms", result.PingMs,
			)
			if err := s.reconcile(true); err != nil {
				return nil, err
			}
			return s.Status()
		}
		if shouldRecoverMissingAddress {
			slog.Info("managed IPv6 addresses missing, running lightweight repair",
				"missing", observation.MissingIPv6Addresses,
			)
			if repairErr := s.repairManagedAddresses(); repairErr != nil {
				slog.Error("repair failed, falling back to full reconcile", "error", repairErr)
				if err := s.reconcile(true); err != nil {
					return nil, err
				}
			}
			return s.Status()
		}
	}
	return status, nil
}

func (s *Service) repairManagedAddresses() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.config.Clone()
	if cfg.Validate() != nil || !cfg.Tunnel.Enabled {
		return nil
	}
	startedAt := time.Now()
	repairErr := s.backend.RepairAddresses(RepairInput{Config: cfg})
	slog.Info("repair completed",
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"error", repairErr,
	)
	observation, observeErr := s.backend.Observe(buildObserveInput(cfg, nil))
	if observation == nil {
		observation = &Observation{}
	}
	s.replaceManagedIPv6AddressReasonsLocked(managedIPv6AddressReasons(observation))
	if repairErr != nil {
		appendErrorReason(&s.state.DegradedReasons, repairErr)
	}
	if observeErr != nil {
		appendErrorReason(&s.state.DegradedReasons, observeErr)
	}
	if repairErr == nil && observeErr == nil && !hasMissingManagedIPv6Addresses(observation) {
		if observation.TunnelUp && len(s.state.DegradedReasons) == 0 {
			s.state.ReconcileState = tunnel.ReconcileReady
		}
	} else {
		s.state.ReconcileState = tunnel.ReconcileDegraded
	}
	s.state.LastError = firstReason(s.state.DegradedReasons)
	if err := SaveState(s.statePath, s.state); err != nil {
		return err
	}
	if repairErr != nil {
		return repairErr
	}
	return observeErr
}

func (s *Service) reconcile(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileLocked(force)
}

func (s *Service) reconcileLocked(force bool) error {
	document := s.document.Clone()
	cfg := s.config.Clone()
	validationErr := cfg.Validate()
	validationReasons := validationReasons(validationErr)
	previousApplied := s.state.Applied
	reconcileStart := time.Now()
	slog.Info("reconcile started",
		"force", force,
		"tunnel_enabled", document.TunnelEnabled,
		"active_profile", document.ActiveProfileID,
		"config_valid", len(validationReasons) == 0,
	)
	s.state.Interface = tunnel.InterfaceName
	s.state.DesiredEnabled = document.TunnelEnabled
	s.state.ActiveProfileID = document.ActiveProfileID
	s.state.ActiveProfileName = ""
	s.state.ActiveBroker = ""
	if activeProfile, err := document.ActiveProfile(); err == nil {
		s.state.ActiveProfileName = activeProfile.Name
		s.state.ActiveBroker = activeProfile.Config.Tunnel.Broker
	}
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
	observation, observeErr := s.backend.Observe(buildObserveInput(cfg, validationReasons))
	if observation == nil {
		observation = &Observation{}
	}
	probeResult := ProbeResult{}
	probeTime := time.Time{}
	if len(validationReasons) == 0 && cfg.Tunnel.Enabled && cfg.Health.Enabled && cfg.Health.Target != "" {
		result, err := s.backend.Probe(cfg.Health.Target)
		if err == nil {
			probeResult = result
			probeTime = time.Now().UTC()
		}
	}
	s.state.WANIPv4 = observation.WANIPv4
	s.state.LocalIPv6 = cfg.Tunnel.LocalIPv6
	s.state.EffectiveMTU = 0
	if len(validationReasons) == 0 && cfg.Tunnel.Enabled && cfg.Tunnel.MTU > 0 {
		s.state.EffectiveMTU = cfg.Tunnel.MTU
	}
	s.state.Networks = buildAppliedState(cfg).Networks
	s.state.TunnelUp = observation.TunnelUp
	s.state.PingOK = probeResult.PingOK
	s.state.PingMs = probeResult.PingMs
	if !probeTime.IsZero() {
		s.state.LastPingAt = probeTime
	}
	s.state.DegradedReasons = validationReasons
	appendManagedIPv6AddressReasons(&s.state.DegradedReasons, observation)
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
	slog.Info("reconcile finished",
		"duration_ms", time.Since(reconcileStart).Milliseconds(),
		"backend_error", backendErr,
		"advertiser_error", advertiserErr,
		"observe_error", observeErr,
		"probe_ok", probeResult.PingOK,
		"degraded_reasons", s.state.DegradedReasons,
	)
	switch {
	case !cfg.Tunnel.Enabled:
		s.state.ReconcileState = tunnel.ReconcileDown
		s.state.Applied = emptyAppliedState()
	case len(validationReasons) > 0:
		s.state.ReconcileState = tunnel.ReconcileDegraded
	case backendErr != nil || advertiserErr != nil || observeErr != nil || hasMissingManagedIPv6Addresses(observation):
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

func (s *Service) rollbackConfigUpdateLocked(previousDocument *config.Document, previousConfig *config.Config, previousState *StateDocument, updateErr error) error {
	s.document = previousDocument.Clone()
	s.config = previousConfig.Clone()
	s.state = previousState.Clone()
	rollbackErr := s.reconcileLocked(true)
	if rollbackErr == nil {
		return updateErr
	}
	s.document = previousDocument.Clone()
	s.config = previousConfig.Clone()
	s.state = previousState.Clone()
	if err := SaveState(s.statePath, s.state); err != nil {
		return fmt.Errorf("%w; rollback failed: %v; restore state failed: %v", updateErr, rollbackErr, err)
	}
	return fmt.Errorf("%w; rollback failed: %v", updateErr, rollbackErr)
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
	dnsServers, err := resolveRADNSServers(cfg)
	if err != nil {
		return err
	}
	advertiser, err := ra.NewAdvertiser(ra.AdvertiserConfig{
		Networks:  networks,
		DNS:       dnsServers,
		Interval:  10 * time.Second,
		TunnelMTU: cfg.Tunnel.MTU,
	})
	if err != nil {
		return err
	}
	s.advertiser = advertiser
	return nil
}

func resolveRADNSServers(cfg *config.Config) ([]netip.Addr, error) {
	if len(cfg.LAN.DNS) > 0 {
		dnsServers := make([]netip.Addr, 0, len(cfg.LAN.DNS))
		for _, dnsValue := range cfg.LAN.DNS {
			dnsServer, err := netip.ParseAddr(dnsValue)
			if err != nil {
				return nil, fmt.Errorf("parse RA dns %q: %w", dnsValue, err)
			}
			dnsServers = append(dnsServers, dnsServer)
		}
		return dnsServers, nil
	}
	return deriveRouterDNSServers(cfg.LAN.Networks)
}

func buildObserveInput(cfg *config.Config, validationReasons []string) ObserveInput {
	input := ObserveInput{WANInterface: cfg.Server.WANInterface}
	if len(validationReasons) == 0 && cfg.Tunnel.Enabled {
		input.Config = cfg
	}
	return input
}

func appendManagedIPv6AddressReasons(reasons *[]string, observation *Observation) {
	for _, reason := range managedIPv6AddressReasons(observation) {
		if !containsString(*reasons, reason) {
			*reasons = append(*reasons, reason)
		}
	}
}

func managedIPv6AddressReasons(observation *Observation) []string {
	if observation == nil || len(observation.MissingIPv6Addresses) == 0 {
		return []string{}
	}
	reasons := make([]string, 0, len(observation.MissingIPv6Addresses))
	for _, address := range observation.MissingIPv6Addresses {
		reasons = append(reasons, managedIPv6AddressMissingReasonPrefix+address)
	}
	return reasons
}

func hasMissingManagedIPv6Addresses(observation *Observation) bool {
	return observation != nil && len(observation.MissingIPv6Addresses) > 0
}

func deriveRouterDNSServers(networks []config.NetworkConfig) ([]netip.Addr, error) {
	dnsServers := make([]netip.Addr, 0, len(networks))
	seen := make(map[string]struct{}, len(networks))
	for _, network := range networks {
		prefix, err := netip.ParsePrefix(network.Prefix)
		if err != nil {
			return nil, fmt.Errorf("parse auto RA dns prefix %q: %w", network.Prefix, err)
		}
		routerAddress := prefix.Masked().Addr().Next()
		key := routerAddress.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dnsServers = append(dnsServers, routerAddress)
	}
	return dnsServers, nil
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

func (s *Service) replaceManagedIPv6AddressReasonsLocked(reasons []string) {
	filtered := make([]string, 0, len(s.state.DegradedReasons)+len(reasons))
	for _, currentReason := range s.state.DegradedReasons {
		if strings.HasPrefix(currentReason, managedIPv6AddressMissingReasonPrefix) {
			continue
		}
		filtered = append(filtered, currentReason)
	}
	for _, reason := range reasons {
		if !containsString(filtered, reason) {
			filtered = append(filtered, reason)
		}
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

func replaceActiveProfileConfig(document *config.Document, cfg *config.Config) error {
	if document == nil {
		return fmt.Errorf("config document is required")
	}
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	for index := range document.Profiles {
		if document.Profiles[index].ID != document.ActiveProfileID {
			continue
		}
		document.Profiles[index].Config = config.ProfileConfig{
			Tunnel: cfg.Tunnel,
			LAN:    cfg.LAN,
			Health: cfg.Health,
		}
		document.Profiles[index].Config.Tunnel.Enabled = false
		return nil
	}
	return fmt.Errorf("active profile %q not found", document.ActiveProfileID)
}

func preserveDocumentStartupFields(nextDocument *config.Document, currentDocument *config.Document) {
	if nextDocument.Server.Port == 0 {
		nextDocument.Server.Port = currentDocument.Server.Port
	}
}

func loadOrCreateConfigDocument(path string) (*config.Document, *config.Config, error) {
	document, err := config.LoadDocument(path)
	if err == nil {
		cfg, activeErr := document.ActiveConfig()
		if activeErr != nil {
			return nil, nil, activeErr
		}
		return document, cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	cfg := config.Defaults()
	if err := config.Save(path, cfg); err != nil {
		return nil, nil, err
	}
	return config.DocumentFromConfig(cfg, config.StorageFormatLegacy), cfg, nil
}
