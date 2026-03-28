package tunnel

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// NetworkStatus represents a single advertised network.
type NetworkStatus struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
}

// Status holds the current state of the 6in4 tunnel.
type Status struct {
	TunnelUp  bool            `json:"tunnel_up"`
	Interface string          `json:"interface"`
	LocalIPv6 string          `json:"local_ipv6"`
	WANIPv4   string          `json:"wan_ipv4"`
	Networks  []NetworkStatus `json:"networks"`
	PingOK    bool            `json:"ping_ok"`
	PingMs    int             `json:"ping_ms"`
}

// Manager delegates tunnel operations to a shell script.
type Manager struct {
	scriptPath string
}

// NewManager creates a Manager that calls scriptPath for all operations.
func NewManager(scriptPath string) *Manager {
	return &Manager{scriptPath: scriptPath}
}

// Status returns the current tunnel status by calling the script with "status".
func (m *Manager) Status() (*Status, error) {
	out, err := exec.Command(m.scriptPath, "status").Output()
	if err != nil {
		return nil, fmt.Errorf("tunnel status: %w", err)
	}
	var st Status
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}
	return &st, nil
}

// Up brings the tunnel up.
func (m *Manager) Up() error {
	return m.run("up")
}

// Down brings the tunnel down.
func (m *Manager) Down() error {
	return m.run("down")
}

// Restart restarts the tunnel.
func (m *Manager) Restart() error {
	return m.run("restart")
}

func (m *Manager) run(command string) error {
	cmd := exec.Command(m.scriptPath, command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tunnel %s: %w (output: %s)", command, err, string(out))
	}
	return nil
}
