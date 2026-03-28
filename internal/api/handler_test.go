package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

type mockController struct {
	status     *tunnel.Status
	health     *tunnel.Status
	config     *config.Config
	updateErr  error
	lastAction string
	updatedCfg *config.Config
}

func (m *mockController) Status() (*tunnel.Status, error) {
	return m.status, nil
}

func (m *mockController) Health() (*tunnel.Status, error) {
	return m.health, nil
}

func (m *mockController) Up() error {
	m.lastAction = "up"
	return nil
}

func (m *mockController) Down() error {
	m.lastAction = "down"
	return nil
}

func (m *mockController) Restart() error {
	m.lastAction = "restart"
	return nil
}

func (m *mockController) GetConfig() *config.Config {
	return m.config.Clone()
}

func (m *mockController) UpdateConfig(cfg *config.Config) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedCfg = cfg.Clone()
	m.config = cfg.Clone()
	return nil
}

func TestHandleGetStatus(t *testing.T) {
	controller := &mockController{
		status: &tunnel.Status{
			TunnelUp:       true,
			Interface:      tunnel.InterfaceName,
			PingOK:         true,
			PingMs:         42,
			DesiredEnabled: true,
		},
		config: config.Defaults(),
	}
	handler := api.NewHandler(controller)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	handler.HandleGetStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var status tunnel.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !status.TunnelUp {
		t.Error("TunnelUp = false, want true")
	}
	if !status.DesiredEnabled {
		t.Error("DesiredEnabled = false, want true")
	}
}

func TestHandleGetHealth(t *testing.T) {
	controller := &mockController{
		health: &tunnel.Status{PingOK: true, PingMs: 9},
		config: config.Defaults(),
	}
	handler := api.NewHandler(controller)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	handler.HandleGetHealth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var status tunnel.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.PingMs != 9 {
		t.Errorf("PingMs = %d, want 9", status.PingMs)
	}
}

func TestHandleTunnelCommands(t *testing.T) {
	controller := &mockController{config: config.Defaults(), status: &tunnel.Status{}}
	handler := api.NewHandler(controller)
	testCases := []struct {
		method  func(http.ResponseWriter, *http.Request)
		path    string
		command string
	}{
		{method: handler.HandleTunnelUp, path: "/api/tunnel/up", command: "up"},
		{method: handler.HandleTunnelDown, path: "/api/tunnel/down", command: "down"},
		{method: handler.HandleTunnelRestart, path: "/api/tunnel/restart", command: "restart"},
	}
	for _, testCase := range testCases {
		req := httptest.NewRequest(http.MethodPost, testCase.path, nil)
		w := httptest.NewRecorder()
		testCase.method(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", testCase.command, w.Code, http.StatusOK)
		}
		if controller.lastAction != testCase.command {
			t.Fatalf("lastAction = %q, want %q", controller.lastAction, testCase.command)
		}
	}
}

func TestHandleGetConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.Tunnel.Enabled = true
	controller := &mockController{config: cfg}
	handler := api.NewHandler(controller)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	handler.HandleGetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var actual config.Config
	if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !actual.Tunnel.Enabled {
		t.Error("Tunnel.Enabled = false, want true")
	}
}

func TestHandleUpdateConfigPreservesEnabledWhenOmitted(t *testing.T) {
	currentConfig := config.Defaults()
	currentConfig.Tunnel.Enabled = true
	controller := &mockController{config: currentConfig, status: &tunnel.Status{}}
	handler := api.NewHandler(controller)
	body := []byte(`{
		"tunnel": {
			"broker": "he",
			"remote_endpoint": "216.66.88.98",
			"local_ipv6": "2001:470::2/64",
			"ttl": 255,
			"mtu": 1480
		},
		"lan": {
			"enabled": false,
			"dns": ["2606:4700:4700::1111"],
			"mode": "slaac",
			"networks": []
		},
		"health": {
			"enabled": true,
			"interval_sec": 30,
			"target": "2001:4860:4860::8888",
			"auto_restart": true
		},
		"server": {
			"wan_interface": "ppp0",
			"auth_token": "test-token"
		}
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleUpdateConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if controller.updatedCfg == nil {
		t.Fatal("updatedCfg = nil")
	}
	if !controller.updatedCfg.Tunnel.Enabled {
		t.Error("Tunnel.Enabled = false, want preserved true")
	}
}

func TestHandleUpdateConfigValidationError(t *testing.T) {
	controller := &mockController{
		config:    config.Defaults(),
		updateErr: &config.ValidationError{Reasons: []string{"tunnel.remote_endpoint is required when tunnel.enabled=true"}},
	}
	handler := api.NewHandler(controller)
	body, _ := json.Marshal(config.Defaults())
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleUpdateConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestServerAuthMiddleware(t *testing.T) {
	controller := &mockController{
		config: config.Defaults(),
		status: &tunnel.Status{},
	}
	server := api.NewServer(api.NewHandler(controller), func() string { return "secret-token" }, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestControlServerSkipsExternalAuth(t *testing.T) {
	controller := &mockController{
		config: config.Defaults(),
		status: &tunnel.Status{},
	}
	server := api.NewControlServer(api.NewHandler(controller))
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
