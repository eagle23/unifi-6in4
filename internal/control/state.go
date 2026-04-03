package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/fileutil"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

// AppliedState describes the last desired dataplane snapshot applied by the daemon.
type AppliedState struct {
	WANInterface   string                 `json:"wan_interface,omitempty"`
	RemoteEndpoint string                 `json:"remote_endpoint,omitempty"`
	LocalIPv6      string                 `json:"local_ipv6,omitempty"`
	LANEnabled     bool                   `json:"lan_enabled"`
	Networks       []tunnel.NetworkStatus `json:"networks,omitempty"`
}

// StateDocument is the persisted observed state document.
type StateDocument struct {
	tunnel.Status
	Applied AppliedState `json:"applied"`
}

// DefaultState returns an empty observed state document.
func DefaultState() *StateDocument {
	return &StateDocument{
		Status: tunnel.Status{
			Interface:      tunnel.InterfaceName,
			Networks:       []tunnel.NetworkStatus{},
			ReconcileState: tunnel.ReconcileIdle,
		},
		Applied: AppliedState{
			Networks: []tunnel.NetworkStatus{},
		},
	}
}

// LoadState loads state.json if present, otherwise returns DefaultState.
func LoadState(path string) (*StateDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultState(), nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	state := DefaultState()
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return state, nil
}

// SaveState writes the observed state atomically to disk.
func SaveState(path string, state *StateDocument) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := fileutil.WriteAtomically(path, data); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// CloneStatus returns a deep copy of the public observed status.
func (s *StateDocument) CloneStatus() *tunnel.Status {
	if s == nil {
		return nil
	}
	return s.Status.Clone()
}

// Clone returns a deep copy of the state document.
func (s *StateDocument) Clone() *StateDocument {
	if s == nil {
		return nil
	}
	clone := &StateDocument{
		Status:  *s.Status.Clone(),
		Applied: s.Applied,
	}
	clone.Applied.Networks = append([]tunnel.NetworkStatus(nil), s.Applied.Networks...)
	return clone
}

func buildAppliedState(cfg *config.Config) AppliedState {
	networks := make([]tunnel.NetworkStatus, 0, len(cfg.LAN.Networks))
	for _, network := range cfg.LAN.Networks {
		networks = append(networks, tunnel.NetworkStatus{
			Interface: network.Interface,
			Prefix:    network.Prefix,
		})
	}
	return AppliedState{
		WANInterface:   cfg.Server.WANInterface,
		RemoteEndpoint: cfg.Tunnel.RemoteEndpoint,
		LocalIPv6:      cfg.Tunnel.LocalIPv6,
		LANEnabled:     cfg.LAN.Enabled,
		Networks:       networks,
	}
}
