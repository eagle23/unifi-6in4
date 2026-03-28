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

type mockTunnelManager struct {
	status    *tunnel.Status
	statusErr error
	lastCmd   string
}

func (m *mockTunnelManager) Status() (*tunnel.Status, error) {
	return m.status, m.statusErr
}
func (m *mockTunnelManager) Up() error      { m.lastCmd = "up"; return nil }
func (m *mockTunnelManager) Down() error    { m.lastCmd = "down"; return nil }
func (m *mockTunnelManager) Restart() error { m.lastCmd = "restart"; return nil }

type mockConfigStore struct {
	cfg *config.Config
}

func (m *mockConfigStore) Get() *config.Config             { return m.cfg }
func (m *mockConfigStore) Update(cfg *config.Config) error { m.cfg = cfg; return nil }

func TestHandleGetStatus(t *testing.T) {
	mock := &mockTunnelManager{
		status: &tunnel.Status{TunnelUp: true, Interface: "sit-6in4", WANIPv4: "78.36.199.233", PingOK: true, PingMs: 42},
	}
	h := api.NewHandler(mock, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.HandleGetStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var st tunnel.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !st.TunnelUp {
		t.Error("TunnelUp = false, want true")
	}
	if st.PingMs != 42 {
		t.Errorf("PingMs = %d, want 42", st.PingMs)
	}
}

func TestHandleTunnelUp(t *testing.T) {
	mock := &mockTunnelManager{}
	h := api.NewHandler(mock, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/tunnel/up", nil)
	w := httptest.NewRecorder()
	h.HandleTunnelUp(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if mock.lastCmd != "up" {
		t.Errorf("lastCmd = %q, want %q", mock.lastCmd, "up")
	}
}

func TestHandleTunnelDown(t *testing.T) {
	mock := &mockTunnelManager{}
	h := api.NewHandler(mock, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/tunnel/down", nil)
	w := httptest.NewRecorder()
	h.HandleTunnelDown(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if mock.lastCmd != "down" {
		t.Errorf("lastCmd = %q, want %q", mock.lastCmd, "down")
	}
}

func TestHandleTunnelRestart(t *testing.T) {
	mock := &mockTunnelManager{}
	h := api.NewHandler(mock, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/tunnel/restart", nil)
	w := httptest.NewRecorder()
	h.HandleTunnelRestart(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if mock.lastCmd != "restart" {
		t.Errorf("lastCmd = %q, want %q", mock.lastCmd, "restart")
	}
}

func TestHandleGetConfig(t *testing.T) {
	cs := &mockConfigStore{cfg: config.Defaults()}
	h := api.NewHandler(nil, cs)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	h.HandleGetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var cfg config.Config
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Server.Port != 8686 {
		t.Errorf("Port = %d, want 8686", cfg.Server.Port)
	}
}

func TestHandleUpdateConfig(t *testing.T) {
	cs := &mockConfigStore{cfg: config.Defaults()}
	mock := &mockTunnelManager{}
	h := api.NewHandler(mock, cs)
	newCfg := config.Defaults()
	newCfg.Tunnel.RemoteEndpoint = "1.2.3.4"
	body, _ := json.Marshal(newCfg)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleUpdateConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if cs.cfg.Tunnel.RemoteEndpoint != "1.2.3.4" {
		t.Errorf("RemoteEndpoint = %q, want %q", cs.cfg.Tunnel.RemoteEndpoint, "1.2.3.4")
	}
	if mock.lastCmd != "restart" {
		t.Errorf("lastCmd = %q, want %q", mock.lastCmd, "restart")
	}
}

func TestAuthMiddlewareRejectsNoToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := api.AuthMiddleware("secret-token", inner)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareAcceptsValidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := api.AuthMiddleware("secret-token", inner)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAuthMiddlewareSkipsWhenEmpty(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := api.AuthMiddleware("", inner)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (no auth when token empty)", w.Code, http.StatusOK)
	}
}
