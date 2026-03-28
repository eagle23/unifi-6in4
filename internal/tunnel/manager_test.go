package tunnel_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

func TestClientStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/status")
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		status := tunnel.Status{
			TunnelUp:       true,
			Interface:      tunnel.InterfaceName,
			WANIPv4:        "78.36.199.233",
			LocalIPv6:      "2001:470::2/64",
			PingOK:         true,
			PingMs:         42,
			ConfigValid:    true,
			DesiredEnabled: true,
			ReconcileState: "ready",
		}
		_ = json.NewEncoder(w).Encode(status)
	}))
	defer server.Close()
	client := tunnel.NewClient(tunnel.ClientConfig{
		BaseURL: server.URL,
		Token:   "secret-token",
	})
	status, err := client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if !status.TunnelUp {
		t.Error("TunnelUp = false, want true")
	}
	if status.ReconcileState != "ready" {
		t.Errorf("ReconcileState = %q, want %q", status.ReconcileState, "ready")
	}
}

func TestClientCommands(t *testing.T) {
	commands := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		commands = append(commands, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	client := tunnel.NewClient(tunnel.ClientConfig{BaseURL: server.URL})
	if err := client.Up(); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	if err := client.Down(); err != nil {
		t.Fatalf("Down() error: %v", err)
	}
	if err := client.Restart(); err != nil {
		t.Fatalf("Restart() error: %v", err)
	}
	expected := []string{
		"POST /api/tunnel/up",
		"POST /api/tunnel/down",
		"POST /api/tunnel/restart",
	}
	if len(commands) != len(expected) {
		t.Fatalf("len(commands) = %d, want %d", len(commands), len(expected))
	}
	for index, command := range commands {
		if command != expected[index] {
			t.Errorf("command[%d] = %q, want %q", index, command, expected[index])
		}
	}
}

func TestClientHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/health")
		}
		_ = json.NewEncoder(w).Encode(tunnel.Status{PingOK: true, PingMs: 7})
	}))
	defer server.Close()
	client := tunnel.NewClient(tunnel.ClientConfig{BaseURL: server.URL})
	status, err := client.Health()
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if !status.PingOK {
		t.Error("PingOK = false, want true")
	}
	if status.PingMs != 7 {
		t.Errorf("PingMs = %d, want 7", status.PingMs)
	}
}

func TestClientStatusViaUnixSocket(t *testing.T) {
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ipv6-tunnel-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix) error: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/status" {
				t.Fatalf("path = %q, want %q", r.URL.Path, "/api/status")
			}
			_ = json.NewEncoder(w).Encode(tunnel.Status{
				TunnelUp:       true,
				DesiredEnabled: true,
				ReconcileState: "ready",
			})
		}),
	}
	defer server.Close()
	go func() {
		_ = server.Serve(listener)
	}()
	client := tunnel.NewClient(tunnel.ClientConfig{
		SocketPath: socketPath,
	})
	status, err := client.Status()
	if err != nil {
		t.Fatalf("Status() via socket error: %v", err)
	}
	if !status.TunnelUp {
		t.Error("TunnelUp = false, want true")
	}
}
