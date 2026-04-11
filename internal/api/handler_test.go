package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/logging"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
	webui "github.com/eagle23/unifi-tunnel-4to6/web"
)

type mockController struct {
	status          *tunnel.Status
	health          *tunnel.Status
	document        *config.Document
	updateErr       error
	lastAction      string
	updatedDocument *config.Document
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

func (m *mockController) GetConfigDocument() *config.Document {
	return m.document.Clone()
}

func (m *mockController) UpdateConfigDocument(document *config.Document) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedDocument = document.Clone()
	m.document = document.Clone()
	return nil
}

func TestHandleGetStatus(t *testing.T) {
	controller := &mockController{
		status: &tunnel.Status{
			TunnelUp:          true,
			Interface:         tunnel.InterfaceName,
			PingOK:            true,
			PingMs:            42,
			DesiredEnabled:    true,
			ActiveProfileID:   "he-home",
			ActiveProfileName: "HE Home",
			ActiveBroker:      "he",
		},
		document: config.DefaultDocument(),
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
	if status.ActiveProfileID != "he-home" {
		t.Errorf("ActiveProfileID = %q, want %q", status.ActiveProfileID, "he-home")
	}
}

func TestHandleGetHealth(t *testing.T) {
	expectedLastPingAt := time.Date(2026, time.April, 3, 17, 19, 19, 0, time.UTC)
	controller := &mockController{
		health:   &tunnel.Status{PingOK: true, PingMs: 9, LastPingAt: expectedLastPingAt},
		document: config.DefaultDocument(),
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
	if !status.LastPingAt.Equal(expectedLastPingAt) {
		t.Errorf("LastPingAt = %s, want %s", status.LastPingAt, expectedLastPingAt)
	}
}

func TestHandleTunnelCommands(t *testing.T) {
	controller := &mockController{document: config.DefaultDocument(), status: &tunnel.Status{}}
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
	document := config.DefaultDocument()
	document.StorageFormat = config.StorageFormatProfiles
	document.TunnelEnabled = true
	document.ActiveProfileID = "he-home"
	document.Profiles = []config.Profile{{
		ID:   "he-home",
		Name: "HE Home",
		Config: config.ProfileConfig{
			Tunnel: config.TunnelConfig{
				Broker:         "he",
				RemoteEndpoint: "216.66.88.98",
				LocalIPv6:      "2001:470::2/64",
				RemoteIPv6:     "2001:470::1/64",
				TTL:            255,
				MTU:            1480,
			},
			LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
			Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
		},
	}}
	controller := &mockController{document: document}
	handler := api.NewHandler(controller)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	handler.HandleGetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var actual config.Document
	if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !actual.TunnelEnabled {
		t.Error("TunnelEnabled = false, want true")
	}
	if actual.ActiveProfileID != "he-home" {
		t.Errorf("ActiveProfileID = %q, want %q", actual.ActiveProfileID, "he-home")
	}
	if len(actual.Profiles) != 1 || actual.Profiles[0].Name != "HE Home" {
		t.Fatalf("Profiles = %+v, want one HE Home profile", actual.Profiles)
	}
}

func TestHandleUpdateConfigAcceptsDocument(t *testing.T) {
	currentDocument := config.DefaultDocument()
	currentDocument.StorageFormat = config.StorageFormatProfiles
	currentDocument.TunnelEnabled = true
	currentDocument.ActiveProfileID = "he-home"
	currentDocument.Profiles = []config.Profile{{
		ID:   "he-home",
		Name: "HE Home",
		Config: config.ProfileConfig{
			Tunnel: config.TunnelConfig{
				Broker:         "he",
				RemoteEndpoint: "216.66.88.98",
				LocalIPv6:      "2001:470::2/64",
				RemoteIPv6:     "2001:470::1/64",
				TTL:            255,
				MTU:            1480,
			},
			LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
			Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
		},
	}}
	controller := &mockController{document: currentDocument, status: &tunnel.Status{}}
	handler := api.NewHandler(controller)
	body := []byte(`{
		"storage_format": "profiles",
		"tunnel_enabled": true,
		"active_profile_id": "backup",
		"profiles": [
			{
				"id": "he-home",
				"name": "HE Home",
				"config": {
					"tunnel": {"broker":"he","remote_endpoint":"216.66.88.98","local_ipv6":"2001:470::2/64","remote_ipv6":"2001:470::1/64","ttl":255,"mtu":1480},
					"lan": {"enabled":false,"dns":[],"mode":"slaac","networks":[]},
					"health": {"enabled":true,"interval_sec":30,"target":"2001:4860:4860::8888","auto_restart":true}
				}
			},
			{
				"id": "backup",
				"name": "Backup",
				"config": {
					"tunnel": {"broker":"custom","remote_endpoint":"198.51.100.10","local_ipv6":"2001:db8::2/64","remote_ipv6":"2001:db8::1/64","ttl":255,"mtu":0},
					"lan": {"enabled":false,"dns":[],"mode":"slaac","networks":[]},
					"health": {"enabled":true,"interval_sec":30,"target":"2001:4860:4860::8888","auto_restart":true}
				}
			}
		],
		"server": {
			"port": 9086,
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
	if controller.updatedDocument == nil {
		t.Fatal("updatedDocument = nil")
	}
	if controller.updatedDocument.ActiveProfileID != "backup" {
		t.Errorf("ActiveProfileID = %q, want %q", controller.updatedDocument.ActiveProfileID, "backup")
	}
	if len(controller.updatedDocument.Profiles) != 2 {
		t.Fatalf("len(Profiles) = %d, want 2", len(controller.updatedDocument.Profiles))
	}
}

func TestHandleUpdateConfigWithLegacyPayloadPreservesExistingProfiles(t *testing.T) {
	currentDocument := config.DefaultDocument()
	currentDocument.StorageFormat = config.StorageFormatProfiles
	currentDocument.TunnelEnabled = true
	currentDocument.ActiveProfileID = "primary"
	currentDocument.Server.Port = 9876
	currentDocument.Profiles = []config.Profile{
		{
			ID:   "primary",
			Name: "Primary",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{
					Broker:         "he",
					RemoteEndpoint: "216.66.88.98",
					LocalIPv6:      "2001:470::2/64",
					RemoteIPv6:     "2001:470::1/64",
					TTL:            255,
					MTU:            1480,
				},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
		{
			ID:   "backup",
			Name: "Backup",
			Config: config.ProfileConfig{
				Tunnel: config.TunnelConfig{
					Broker:         "custom",
					RemoteEndpoint: "198.51.100.10",
					LocalIPv6:      "2001:db8::2/64",
					RemoteIPv6:     "2001:db8::1/64",
					TTL:            255,
					MTU:            0,
				},
				LAN:    config.LANConfig{Enabled: false, DNS: []string{}, Mode: "slaac", Networks: []config.NetworkConfig{}},
				Health: config.HealthConfig{Enabled: true, IntervalSec: 30, Target: "2001:4860:4860::8888", AutoRestart: true},
			},
		},
	}
	controller := &mockController{document: currentDocument, status: &tunnel.Status{}}
	handler := api.NewHandler(controller)
	body := []byte(`{
		"tunnel": {
			"enabled": true,
			"broker": "ip4market",
			"remote_endpoint": "203.0.113.5",
			"local_ipv6": "2001:db8:ffff::2/64",
			"remote_ipv6": "2001:db8:ffff::1/64",
			"ttl": 64,
			"mtu": 1472
		},
		"lan": {
			"enabled": false,
			"dns": [],
			"mode": "slaac",
			"networks": []
		},
		"health": {
			"enabled": true,
			"interval_sec": 45,
			"target": "2001:4860:4860::8888",
			"auto_restart": false
		},
		"server": {
			"wan_interface": "ppp0",
			"auth_token": "new-token"
		}
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleUpdateConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if controller.updatedDocument == nil {
		t.Fatal("updatedDocument = nil")
	}
	if controller.updatedDocument.StorageFormat != config.StorageFormatProfiles {
		t.Fatalf("StorageFormat = %q, want %q", controller.updatedDocument.StorageFormat, config.StorageFormatProfiles)
	}
	if len(controller.updatedDocument.Profiles) != 2 {
		t.Fatalf("len(Profiles) = %d, want 2", len(controller.updatedDocument.Profiles))
	}
	if controller.updatedDocument.ActiveProfileID != "primary" {
		t.Fatalf("ActiveProfileID = %q, want %q", controller.updatedDocument.ActiveProfileID, "primary")
	}
	if controller.updatedDocument.Server.Port != 9876 {
		t.Fatalf("Server.Port = %d, want 9876", controller.updatedDocument.Server.Port)
	}
	if controller.updatedDocument.Profiles[1].ID != "backup" {
		t.Fatalf("Profiles[1].ID = %q, want backup", controller.updatedDocument.Profiles[1].ID)
	}
}

func TestHandleUpdateConfigValidationError(t *testing.T) {
	controller := &mockController{
		document:  config.DefaultDocument(),
		updateErr: &config.ValidationError{Reasons: []string{"tunnel.remote_endpoint is required when tunnel.enabled=true"}},
	}
	handler := api.NewHandler(controller)
	body, _ := json.Marshal(config.DefaultDocument())
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleUpdateConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestServerAuthMiddleware(t *testing.T) {
	controller := &mockController{
		document: config.DefaultDocument(),
		status:   &tunnel.Status{},
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
		document: config.DefaultDocument(),
		status:   &tunnel.Status{},
	}
	server := api.NewControlServer(api.NewHandler(controller))
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetLogsReturnsTailedEntries(t *testing.T) {
	dir := t.TempDir()
	if err := logging.Init(dir); err != nil {
		t.Fatalf("logging.Init() error: %v", err)
	}
	defer logging.Close()
	slog.Info("reconcile started", "force", true)
	slog.Warn("probe failed")
	controller := &mockController{
		document: config.DefaultDocument(),
		status:   &tunnel.Status{},
	}
	handler := api.NewHandler(controller)
	req := httptest.NewRequest(http.MethodGet, "/api/logs?tail=50", nil)
	w := httptest.NewRecorder()
	handler.HandleGetLogs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var body struct {
		Path  string   `json:"path"`
		Lines []string `json:"lines"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Path == "" {
		t.Fatal("expected path to be populated")
	}
	if len(body.Lines) < 2 {
		t.Fatalf("lines = %d, want >= 2", len(body.Lines))
	}
	joined := body.Lines[0] + "\n" + body.Lines[len(body.Lines)-1]
	if !bytes.Contains([]byte(joined), []byte("reconcile started")) {
		t.Fatalf("first entry missing: %v", body.Lines)
	}
}

func TestHandleGetLogsRejectsInvalidTail(t *testing.T) {
	controller := &mockController{
		document: config.DefaultDocument(),
		status:   &tunnel.Status{},
	}
	handler := api.NewHandler(controller)
	req := httptest.NewRequest(http.MethodGet, "/api/logs?tail=abc", nil)
	w := httptest.NewRecorder()
	handler.HandleGetLogs(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestServerServesEmbeddedUI(t *testing.T) {
	controller := &mockController{
		document: config.DefaultDocument(),
		status:   &tunnel.Status{},
	}
	server := api.NewServer(api.NewHandler(controller), func() string { return "" }, webui.FileSystem())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("IPv6 Tunnel Manager")) {
		t.Fatalf("body does not contain embedded UI marker")
	}
}
