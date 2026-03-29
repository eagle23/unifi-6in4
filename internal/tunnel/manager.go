package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	InterfaceName string = "sit-6in4"
	defaultURL    string = "http://127.0.0.1:9086"
	unixBaseURL   string = "http://unix"

	ReconcileIdle        = "idle"
	ReconcileReconciling = "reconciling"
	ReconcileReady       = "ready"
	ReconcileDegraded    = "degraded"
	ReconcileDown        = "down"

	ReasonHealthProbeFailed = "health probe failed"
)

// NetworkStatus represents a single advertised network.
type NetworkStatus struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
}

// Status holds the current state of the 6in4 tunnel.
type Status struct {
	TunnelUp        bool            `json:"tunnel_up"`
	Interface       string          `json:"interface"`
	LocalIPv6       string          `json:"local_ipv6"`
	EffectiveMTU    int             `json:"effective_mtu"`
	WANIPv4         string          `json:"wan_ipv4"`
	Networks        []NetworkStatus `json:"networks"`
	PingOK          bool            `json:"ping_ok"`
	PingMs          int             `json:"ping_ms"`
	ConfigValid     bool            `json:"config_valid"`
	DesiredEnabled  bool            `json:"desired_enabled"`
	ReconcileState  string          `json:"reconcile_state"`
	LastError       string          `json:"last_error"`
	DegradedReasons []string        `json:"degraded_reasons"`
	LastReconcileAt time.Time       `json:"last_reconcile_at"`
}

// Clone returns a deep copy of Status.
func (s *Status) Clone() *Status {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Networks = append([]NetworkStatus(nil), s.Networks...)
	clone.DegradedReasons = append([]string(nil), s.DegradedReasons...)
	return &clone
}

// ClientConfig holds configuration for the local control client.
type ClientConfig struct {
	BaseURL    string
	SocketPath string
	Token      string
	HTTPClient *http.Client
}

// Client talks to the local daemon over HTTP.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a local control client.
func NewClient(cfg ClientConfig) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		if cfg.SocketPath != "" {
			baseURL = unixBaseURL
		} else {
			baseURL = defaultURL
		}
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.SocketPath != "" {
			socketPath := cfg.SocketPath
			transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: 5 * time.Second}
				return dialer.DialContext(ctx, "unix", socketPath)
			}
		}
		httpClient = &http.Client{
			Timeout:   5 * time.Second,
			Transport: transport,
		}
	}
	return &Client{
		baseURL:    baseURL,
		token:      cfg.Token,
		httpClient: httpClient,
	}
}

// Status fetches the current tunnel status from the daemon.
func (c *Client) Status() (*Status, error) {
	var status Status
	if err := c.requestJSON(http.MethodGet, "/api/status", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// Health triggers a fresh health probe and returns the updated status.
func (c *Client) Health() (*Status, error) {
	var status Status
	if err := c.requestJSON(http.MethodGet, "/api/health", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// Up enables the desired tunnel state.
func (c *Client) Up() error {
	return c.requestJSON(http.MethodPost, "/api/tunnel/up", nil, nil)
}

// Down disables the desired tunnel state.
func (c *Client) Down() error {
	return c.requestJSON(http.MethodPost, "/api/tunnel/down", nil, nil)
}

// Restart forces a reconcile of the tunnel dataplane.
func (c *Client) Restart() error {
	return c.requestJSON(http.MethodPost, "/api/tunnel/restart", nil, nil)
}

func (c *Client) requestJSON(method string, path string, body io.Reader, target any) error {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= http.StatusBadRequest {
		responseBody, readErr := io.ReadAll(res.Body)
		if readErr != nil {
			return fmt.Errorf("request failed with status %d", res.StatusCode)
		}
		return fmt.Errorf("request failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
