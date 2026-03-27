# IPv6 6in4 Tunnel Manager — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a 6in4 tunnel manager for UniFi UCG-Fiber with a Go HTTP server, shell-based tunnel control, Router Advertisement support, and a web UI.

**Architecture:** A static Go binary (ARM64) serves a REST API and embedded SPA on port 8686. It delegates tunnel operations to `tunnel.sh` via `exec.Command`. The Go server also handles Router Advertisement (RA) sending via `mdlayher/ndp`. Config lives in `/data/ipv6-tunnel/config.json`, boot persistence via `/etc/rc.local`.

**Tech Stack:** Go 1.22+, `mdlayher/ndp` (RA), `embed` (SPA), vanilla HTML/CSS/JS, bash, `ip`/`nftables`/`sysctl` CLI tools.

---

## File Structure

```
unifi-tunnel-4to6/
├── cmd/
│   └── server/
│       └── main.go                 ← entry point, wires config+tunnel+ra+api
├── internal/
│   ├── config/
│   │   ├── config.go               ← Config struct, Load(), Save(), Defaults()
│   │   └── config_test.go          ← unit tests
│   ├── tunnel/
│   │   ├── manager.go              ← TunnelManager: Up/Down/Restart/Status via exec
│   │   └── manager_test.go         ← unit tests with mock script
│   ├── ra/
│   │   ├── advertiser.go           ← RAAdvertiser: periodic RA on LAN interfaces
│   │   └── advertiser_test.go      ← unit tests
│   └── api/
│       ├── server.go               ← NewServer(), routes, auth middleware, embed
│       ├── handler.go              ← handler methods: status, config, tunnel, health
│       └── handler_test.go         ← httptest-based tests
├── web/
│   └── index.html                  ← SPA: status dashboard + settings form
├── scripts/
│   ├── tunnel.sh                   ← up/down/restart/status/boot
│   ├── install.sh                  ← one-shot installer for router
│   └── rc-local-fragment.sh        ← fragment to append to /etc/rc.local
├── Makefile                        ← build, deploy, clean targets
├── go.mod
└── go.sum
```

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `.gitignore`

- [ ] **Step 1: Initialize git repo**

```bash
cd /Users/eagle23/Documents/vpn/unifi-tunnel-4to6
git init
```

- [ ] **Step 2: Create .gitignore**

Create `.gitignore`:

```gitignore
bin/
*.exe
.DS_Store
```

- [ ] **Step 3: Initialize Go module**

```bash
go mod init github.com/eagle23/unifi-tunnel-4to6
```

- [ ] **Step 4: Create Makefile**

Create `Makefile`:

```makefile
BINARY_NAME=ipv6-tunnel-server
ROUTER_HOST?=192.168.1.1
ROUTER_USER?=root
REMOTE_DIR=/data/ipv6-tunnel

.PHONY: build test deploy clean

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
		-ldflags="-s -w" \
		-o bin/$(BINARY_NAME) ./cmd/server/

test:
	go test ./... -v

deploy: build
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "mkdir -p $(REMOTE_DIR)/web"
	scp bin/$(BINARY_NAME) $(ROUTER_USER)@$(ROUTER_HOST):$(REMOTE_DIR)/
	scp scripts/tunnel.sh $(ROUTER_USER)@$(ROUTER_HOST):$(REMOTE_DIR)/
	scp web/index.html $(ROUTER_USER)@$(ROUTER_HOST):$(REMOTE_DIR)/web/
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "chmod +x $(REMOTE_DIR)/$(BINARY_NAME) $(REMOTE_DIR)/tunnel.sh"
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "pkill -f $(BINARY_NAME) || true; $(REMOTE_DIR)/$(BINARY_NAME) &"

install: build
	scp scripts/install.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp bin/$(BINARY_NAME) $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp scripts/tunnel.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp scripts/rc-local-fragment.sh $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	scp web/index.html $(ROUTER_USER)@$(ROUTER_HOST):/tmp/
	ssh $(ROUTER_USER)@$(ROUTER_HOST) "chmod +x /tmp/install.sh && /tmp/install.sh"

clean:
	rm -rf bin/
```

- [ ] **Step 5: Create directory structure**

```bash
mkdir -p cmd/server internal/config internal/tunnel internal/ra internal/api web scripts
```

- [ ] **Step 6: Commit**

```bash
git add .gitignore go.mod Makefile
git commit -m "chore: scaffold project structure with go.mod and Makefile"
```

---

### Task 2: Config Package

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test — Load from JSON**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := []byte(`{
		"tunnel": {
			"broker": "he",
			"remote_endpoint": "216.66.88.98",
			"local_ipv6": "2001:470:1f0e:abc::2/64",
			"remote_ipv6": "2001:470:1f0e:abc::1/64",
			"ttl": 255,
			"mtu": 1480
		},
		"lan": {
			"enabled": true,
			"dns": ["2606:4700:4700::1111"],
			"mode": "slaac",
			"networks": [
				{"interface": "br0", "prefix": "2001:470:1f0f:1::/64", "comment": "Default"}
			]
		},
		"health": {
			"enabled": true,
			"interval_sec": 30,
			"target": "2001:4860:4860::8888",
			"auto_restart": true
		},
		"server": {
			"port": 8686,
			"wan_interface": "ppp0",
			"auth_token": "test-token"
		}
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tunnel.Broker != "he" {
		t.Errorf("Broker = %q, want %q", cfg.Tunnel.Broker, "he")
	}
	if cfg.Tunnel.RemoteEndpoint != "216.66.88.98" {
		t.Errorf("RemoteEndpoint = %q, want %q", cfg.Tunnel.RemoteEndpoint, "216.66.88.98")
	}
	if cfg.Tunnel.MTU != 1480 {
		t.Errorf("MTU = %d, want %d", cfg.Tunnel.MTU, 1480)
	}
	if !cfg.LAN.Enabled {
		t.Error("LAN.Enabled = false, want true")
	}
	if len(cfg.LAN.Networks) != 1 {
		t.Fatalf("len(Networks) = %d, want 1", len(cfg.LAN.Networks))
	}
	if cfg.LAN.Networks[0].Interface != "br0" {
		t.Errorf("Networks[0].Interface = %q, want %q", cfg.LAN.Networks[0].Interface, "br0")
	}
	if cfg.Server.Port != 8686 {
		t.Errorf("Port = %d, want %d", cfg.Server.Port, 8686)
	}
	if cfg.Server.AuthToken != "test-token" {
		t.Errorf("AuthToken = %q, want %q", cfg.Server.AuthToken, "test-token")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement Config types and Load()**

Create `internal/config/config.go`:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type TunnelConfig struct {
	Broker         string `json:"broker"`
	RemoteEndpoint string `json:"remote_endpoint"`
	LocalIPv6      string `json:"local_ipv6"`
	RemoteIPv6     string `json:"remote_ipv6"`
	TTL            int    `json:"ttl"`
	MTU            int    `json:"mtu"`
}

type NetworkConfig struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
	Comment   string `json:"comment"`
}

type LANConfig struct {
	Enabled  bool            `json:"enabled"`
	DNS      []string        `json:"dns"`
	Mode     string          `json:"mode"`
	Networks []NetworkConfig `json:"networks"`
}

type HealthConfig struct {
	Enabled     bool   `json:"enabled"`
	IntervalSec int    `json:"interval_sec"`
	Target      string `json:"target"`
	AutoRestart bool   `json:"auto_restart"`
}

type ServerConfig struct {
	Port         int    `json:"port"`
	WANInterface string `json:"wan_interface"`
	AuthToken    string `json:"auth_token"`
}

type Config struct {
	Tunnel TunnelConfig `json:"tunnel"`
	LAN    LANConfig    `json:"lan"`
	Health HealthConfig `json:"health"`
	Server ServerConfig `json:"server"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Write failing test — Save()**

Append to `internal/config/config_test.go`:

```go
func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := &config.Config{
		Tunnel: config.TunnelConfig{
			Broker:         "ip4market",
			RemoteEndpoint: "1.2.3.4",
			LocalIPv6:      "2001:db8::2/64",
			RemoteIPv6:     "2001:db8::1/64",
			TTL:            255,
			MTU:            1480,
		},
		LAN: config.LANConfig{
			Enabled: true,
			DNS:     []string{"2606:4700:4700::1111"},
			Mode:    "slaac",
			Networks: []config.NetworkConfig{
				{Interface: "br0", Prefix: "2001:db8:1::/64", Comment: "Default"},
			},
		},
		Health: config.HealthConfig{
			Enabled:     true,
			IntervalSec: 30,
			Target:      "2001:4860:4860::8888",
			AutoRestart: true,
		},
		Server: config.ServerConfig{
			Port:         8686,
			WANInterface: "ppp0",
			AuthToken:    "secret",
		},
	}

	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if loaded.Tunnel.Broker != "ip4market" {
		t.Errorf("Broker = %q, want %q", loaded.Tunnel.Broker, "ip4market")
	}
	if loaded.Server.AuthToken != "secret" {
		t.Errorf("AuthToken = %q, want %q", loaded.Server.AuthToken, "secret")
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/config/ -v -run TestSaveConfig`
Expected: FAIL — `config.Save` undefined

- [ ] **Step 7: Implement Save()**

Append to `internal/config/config.go`:

```go
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS (both tests)

- [ ] **Step 9: Write failing test — Defaults()**

Append to `internal/config/config_test.go`:

```go
func TestDefaults(t *testing.T) {
	cfg := config.Defaults()

	if cfg.Tunnel.TTL != 255 {
		t.Errorf("TTL = %d, want 255", cfg.Tunnel.TTL)
	}
	if cfg.Tunnel.MTU != 1480 {
		t.Errorf("MTU = %d, want 1480", cfg.Tunnel.MTU)
	}
	if cfg.Server.Port != 8686 {
		t.Errorf("Port = %d, want 8686", cfg.Server.Port)
	}
	if cfg.Server.WANInterface != "ppp0" {
		t.Errorf("WANInterface = %q, want %q", cfg.Server.WANInterface, "ppp0")
	}
	if cfg.Health.IntervalSec != 30 {
		t.Errorf("IntervalSec = %d, want 30", cfg.Health.IntervalSec)
	}
	if cfg.LAN.Mode != "slaac" {
		t.Errorf("Mode = %q, want %q", cfg.LAN.Mode, "slaac")
	}
}
```

- [ ] **Step 10: Run test to verify it fails**

Run: `go test ./internal/config/ -v -run TestDefaults`
Expected: FAIL — `config.Defaults` undefined

- [ ] **Step 11: Implement Defaults()**

Append to `internal/config/config.go`:

```go
func Defaults() *Config {
	return &Config{
		Tunnel: TunnelConfig{
			TTL: 255,
			MTU: 1480,
		},
		LAN: LANConfig{
			Enabled: false,
			DNS:     []string{"2606:4700:4700::1111", "2001:4860:4860::8888"},
			Mode:    "slaac",
		},
		Health: HealthConfig{
			Enabled:     true,
			IntervalSec: 30,
			Target:      "2001:4860:4860::8888",
			AutoRestart: true,
		},
		Server: ServerConfig{
			Port:         8686,
			WANInterface: "ppp0",
		},
	}
}
```

- [ ] **Step 12: Run all tests**

Run: `go test ./internal/config/ -v`
Expected: PASS (all 3 tests)

- [ ] **Step 13: Commit**

```bash
git add internal/config/
git commit -m "feat: add config package with Load, Save, and Defaults"
```

---

### Task 3: Tunnel Manager Package

**Files:**
- Create: `internal/tunnel/manager.go`
- Create: `internal/tunnel/manager_test.go`

- [ ] **Step 1: Write failing test — Status parsing**

Create `internal/tunnel/manager_test.go`:

```go
package tunnel_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

func writeMockScript(t *testing.T, dir string, statusJSON string) string {
	t.Helper()
	script := filepath.Join(dir, "tunnel.sh")
	content := "#!/bin/bash\n"
	content += "case \"$1\" in\n"
	content += "  status)\n"
	content += "    cat <<'STATUSEOF'\n"
	content += statusJSON + "\n"
	content += "STATUSEOF\n"
	content += "    ;;\n"
	content += "  up|down|restart)\n"
	content += "    echo \"ok\"\n"
	content += "    ;;\n"
	content += "esac\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestManagerStatus(t *testing.T) {
	dir := t.TempDir()
	statusJSON := `{"tunnel_up":true,"interface":"sit-6in4","local_ipv6":"2001:470::2","wan_ipv4":"78.36.199.233","networks":[{"interface":"br0","prefix":"2001:470:1::/64"}],"ping_ok":true,"ping_ms":42}`

	scriptPath := writeMockScript(t, dir, statusJSON)
	mgr := tunnel.NewManager(scriptPath)

	st, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if !st.TunnelUp {
		t.Error("TunnelUp = false, want true")
	}
	if st.Interface != "sit-6in4" {
		t.Errorf("Interface = %q, want %q", st.Interface, "sit-6in4")
	}
	if st.WANIPv4 != "78.36.199.233" {
		t.Errorf("WANIPv4 = %q, want %q", st.WANIPv4, "78.36.199.233")
	}
	if !st.PingOK {
		t.Error("PingOK = false, want true")
	}
	if st.PingMs != 42 {
		t.Errorf("PingMs = %d, want 42", st.PingMs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tunnel/ -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement TunnelManager with Status()**

Create `internal/tunnel/manager.go`:

```go
package tunnel

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type NetworkStatus struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
}

type Status struct {
	TunnelUp  bool            `json:"tunnel_up"`
	Interface string          `json:"interface"`
	LocalIPv6 string          `json:"local_ipv6"`
	WANIPv4   string          `json:"wan_ipv4"`
	Networks  []NetworkStatus `json:"networks"`
	PingOK    bool            `json:"ping_ok"`
	PingMs    int             `json:"ping_ms"`
}

type Manager struct {
	scriptPath string
}

func NewManager(scriptPath string) *Manager {
	return &Manager{scriptPath: scriptPath}
}

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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tunnel/ -v`
Expected: PASS

- [ ] **Step 5: Write failing test — Up/Down/Restart**

Append to `internal/tunnel/manager_test.go`:

```go
func TestManagerUp(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeMockScript(t, dir, `{"tunnel_up":false}`)
	mgr := tunnel.NewManager(scriptPath)

	if err := mgr.Up(); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
}

func TestManagerDown(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeMockScript(t, dir, `{"tunnel_up":false}`)
	mgr := tunnel.NewManager(scriptPath)

	if err := mgr.Down(); err != nil {
		t.Fatalf("Down() error: %v", err)
	}
}

func TestManagerRestart(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeMockScript(t, dir, `{"tunnel_up":false}`)
	mgr := tunnel.NewManager(scriptPath)

	if err := mgr.Restart(); err != nil {
		t.Fatalf("Restart() error: %v", err)
	}
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/tunnel/ -v -run "TestManager(Up|Down|Restart)$"`
Expected: FAIL — methods undefined

- [ ] **Step 7: Implement Up(), Down(), Restart()**

Append to `internal/tunnel/manager.go`:

```go
func (m *Manager) Up() error {
	return m.run("up")
}

func (m *Manager) Down() error {
	return m.run("down")
}

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
```

- [ ] **Step 8: Run all tunnel tests**

Run: `go test ./internal/tunnel/ -v`
Expected: PASS (all 4 tests)

- [ ] **Step 9: Commit**

```bash
git add internal/tunnel/
git commit -m "feat: add tunnel manager with Status, Up, Down, Restart"
```

---

### Task 4: Router Advertisement Package

**Files:**
- Create: `internal/ra/advertiser.go`
- Create: `internal/ra/advertiser_test.go`

- [ ] **Step 1: Add ndp dependency**

```bash
go get github.com/mdlayher/ndp
```

- [ ] **Step 2: Write failing test — RA message construction**

Create `internal/ra/advertiser_test.go`:

```go
package ra_test

import (
	"net/netip"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
)

func TestBuildRA(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8:1::/64")
	dns := []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")}

	msg := ra.BuildRA(prefix, dns)

	if msg == nil {
		t.Fatal("BuildRA() returned nil")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/ra/ -v`
Expected: FAIL — package not found

- [ ] **Step 4: Implement BuildRA**

Create `internal/ra/advertiser.go`:

```go
package ra

import (
	"net/netip"
	"time"

	"github.com/mdlayher/ndp"
)

// BuildRA creates a Router Advertisement message with the given prefix and DNS servers.
func BuildRA(prefix netip.Prefix, dnsServers []netip.Addr) *ndp.RouterAdvertisement {
	ra := &ndp.RouterAdvertisement{
		CurrentHopLimit:           64,
		ManagedConfiguration:      false,
		OtherConfiguration:        false,
		RouterSelectionPreference: ndp.Medium,
		RouterLifetime:            30 * time.Minute,
		Options: []ndp.Option{
			&ndp.PrefixInformation{
				PrefixLength:                   uint8(prefix.Bits()),
				OnLink:                         true,
				AutonomousAddressConfiguration: true,
				ValidLifetime:                  2 * time.Hour,
				PreferredLifetime:              30 * time.Minute,
				Prefix:                         prefix.Addr(),
			},
		},
	}
	if len(dnsServers) > 0 {
		ra.Options = append(ra.Options, &ndp.RecursiveDNSServer{
			Lifetime: 30 * time.Minute,
			Servers:  dnsServers,
		})
	}
	return ra
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/ra/ -v`
Expected: PASS

- [ ] **Step 6: Write failing test — Advertiser start/stop lifecycle**

Append to `internal/ra/advertiser_test.go`:

```go
func TestAdvertiserNewAndStop(t *testing.T) {
	cfg := ra.AdvertiserConfig{
		Networks: []ra.NetworkEntry{
			{Interface: "lo", Prefix: netip.MustParsePrefix("2001:db8:1::/64")},
		},
		DNS:      []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")},
		Interval: 100 * time.Millisecond,
	}

	adv, err := ra.NewAdvertiser(cfg)
	if err != nil {
		// On non-Linux or unprivileged: skip
		t.Skipf("NewAdvertiser() error (expected on non-Linux): %v", err)
	}
	adv.Stop()
}
```

Add `"time"` to the import block.

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./internal/ra/ -v -run TestAdvertiserNewAndStop`
Expected: FAIL — `ra.AdvertiserConfig` undefined

- [ ] **Step 8: Implement Advertiser struct with Start/Stop**

Append to `internal/ra/advertiser.go`:

```go
import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mdlayher/ndp"
)

type NetworkEntry struct {
	Interface string
	Prefix    netip.Prefix
}

type AdvertiserConfig struct {
	Networks []NetworkEntry
	DNS      []netip.Addr
	Interval time.Duration
}

type connEntry struct {
	conn   *ndp.Conn
	iface  *net.Interface
	prefix netip.Prefix
}

type Advertiser struct {
	conns  []connEntry
	dns    []netip.Addr
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewAdvertiser(cfg AdvertiserConfig) (*Advertiser, error) {
	var conns []connEntry
	for _, nw := range cfg.Networks {
		iface, err := net.InterfaceByName(nw.Interface)
		if err != nil {
			return nil, fmt.Errorf("interface %q: %w", nw.Interface, err)
		}
		conn, _, err := ndp.Listen(iface, ndp.LinkLocal)
		if err != nil {
			for _, c := range conns {
				c.conn.Close()
			}
			return nil, fmt.Errorf("ndp listen on %q: %w", nw.Interface, err)
		}
		conns = append(conns, connEntry{conn: conn, iface: iface, prefix: nw.Prefix})
	}
	ctx, cancel := context.WithCancel(context.Background())
	adv := &Advertiser{
		conns:  conns,
		dns:    cfg.DNS,
		cancel: cancel,
	}
	interval := cfg.Interval
	if interval == 0 {
		interval = 10 * time.Second
	}
	for _, ce := range conns {
		adv.wg.Add(1)
		go adv.loop(ctx, ce, interval)
	}
	return adv, nil
}

func (a *Advertiser) loop(ctx context.Context, ce connEntry, interval time.Duration) {
	defer a.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	allNodes := netip.MustParseAddr("ff02::1")
	for {
		msg := BuildRA(ce.prefix, a.dns)
		if err := ce.conn.WriteTo(msg, nil, allNodes); err != nil {
			log.Printf("ra: send on %s: %v", ce.iface.Name, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Advertiser) Stop() {
	a.cancel()
	a.wg.Wait()
	for _, ce := range a.conns {
		ce.conn.Close()
	}
}
```

Note: the import block at the top of the file should be consolidated into one. The full file will have a single import block with all needed packages.

- [ ] **Step 9: Run all RA tests**

Run: `go test ./internal/ra/ -v`
Expected: PASS (TestBuildRA passes, TestAdvertiserNewAndStop skips on macOS due to raw socket)

- [ ] **Step 10: Tidy modules**

```bash
go mod tidy
```

- [ ] **Step 11: Commit**

```bash
git add internal/ra/ go.mod go.sum
git commit -m "feat: add Router Advertisement advertiser using mdlayher/ndp"
```

---

### Task 5: API Handlers

**Files:**
- Create: `internal/api/handler.go`
- Create: `internal/api/handler_test.go`

- [ ] **Step 1: Write failing test — GET /api/status**

Create `internal/api/handler_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
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

func (m *mockTunnelManager) Up() error {
	m.lastCmd = "up"
	return nil
}

func (m *mockTunnelManager) Down() error {
	m.lastCmd = "down"
	return nil
}

func (m *mockTunnelManager) Restart() error {
	m.lastCmd = "restart"
	return nil
}

func TestHandleGetStatus(t *testing.T) {
	mock := &mockTunnelManager{
		status: &tunnel.Status{
			TunnelUp:  true,
			Interface: "sit-6in4",
			WANIPv4:   "78.36.199.233",
			PingOK:    true,
			PingMs:    42,
		},
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement Handler with HandleGetStatus**

Create `internal/api/handler.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

type TunnelController interface {
	Status() (*tunnel.Status, error)
	Up() error
	Down() error
	Restart() error
}

type ConfigStore interface {
	Get() *config.Config
	Update(cfg *config.Config) error
}

type Handler struct {
	tunnel TunnelController
	config ConfigStore
}

func NewHandler(tc TunnelController, cs ConfigStore) *Handler {
	return &Handler{tunnel: tc, config: cs}
}

func (h *Handler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.tunnel.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -v`
Expected: PASS

- [ ] **Step 5: Write failing tests — tunnel control + config endpoints**

Append to `internal/api/handler_test.go`:

```go
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

type mockConfigStore struct {
	cfg *config.Config
}

func (m *mockConfigStore) Get() *config.Config {
	return m.cfg
}

func (m *mockConfigStore) Update(cfg *config.Config) error {
	m.cfg = cfg
	return nil
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
		t.Errorf("lastCmd = %q, want %q (expected restart after config update)", mock.lastCmd, "restart")
	}
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/api/ -v`
Expected: FAIL — undefined methods

- [ ] **Step 7: Implement remaining handler methods**

Append to `internal/api/handler.go`:

```go
func (h *Handler) HandleTunnelUp(w http.ResponseWriter, r *http.Request) {
	if err := h.tunnel.Up(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) HandleTunnelDown(w http.ResponseWriter, r *http.Request) {
	if err := h.tunnel.Down(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) HandleTunnelRestart(w http.ResponseWriter, r *http.Request) {
	if err := h.tunnel.Restart(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.config.Get())
}

func (h *Handler) HandleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.config.Update(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.tunnel.Restart(); err != nil {
		http.Error(w, "config saved but tunnel restart failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

- [ ] **Step 8: Run all API tests**

Run: `go test ./internal/api/ -v`
Expected: PASS (all 6 tests)

- [ ] **Step 9: Commit**

```bash
git add internal/api/
git commit -m "feat: add API handlers for status, config, and tunnel control"
```

---

### Task 6: HTTP Server with Auth and Static Files

**Files:**
- Create: `internal/api/server.go`
- Modify: `internal/api/handler_test.go` (add auth test)

- [ ] **Step 1: Write failing test — auth middleware**

Append to `internal/api/handler_test.go`:

```go
func TestAuthMiddlewareRejectsNoToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := api.AuthMiddleware("secret-token", inner)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareAcceptsValidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
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
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := api.AuthMiddleware("", inner)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (no auth when token empty)", w.Code, http.StatusOK)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -v -run TestAuthMiddleware`
Expected: FAIL — `api.AuthMiddleware` undefined

- [ ] **Step 3: Implement server.go with AuthMiddleware and NewServer**

Create `internal/api/server.go`:

```go
package api

import (
	"io/fs"
	"net/http"
	"strings"
)

func AuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func NewServer(h *Handler, token string, webFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/status", h.HandleGetStatus)
	apiMux.HandleFunc("GET /api/config", h.HandleGetConfig)
	apiMux.HandleFunc("PUT /api/config", h.HandleUpdateConfig)
	apiMux.HandleFunc("POST /api/tunnel/up", h.HandleTunnelUp)
	apiMux.HandleFunc("POST /api/tunnel/down", h.HandleTunnelDown)
	apiMux.HandleFunc("POST /api/tunnel/restart", h.HandleTunnelRestart)

	mux.Handle("/api/", AuthMiddleware(token, apiMux))

	if webFS != nil {
		mux.Handle("/", http.FileServer(http.FS(webFS)))
	}

	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run all API tests**

Run: `go test ./internal/api/ -v`
Expected: PASS (all tests including auth middleware)

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go
git commit -m "feat: add HTTP server with auth middleware and CORS"
```

---

### Task 7: tunnel.sh Script

**Files:**
- Create: `scripts/tunnel.sh`

- [ ] **Step 1: Create tunnel.sh**

Create `scripts/tunnel.sh`:

```bash
#!/bin/bash
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG_FILE="${SCRIPT_DIR}/config.json"
TUNNEL_IFACE="sit-6in4"
SERVER_BIN="${SCRIPT_DIR}/ipv6-tunnel-server"

log_message() {
    logger -t ipv6-tunnel "$*"
}

read_config() {
    if [ ! -f "$CONFIG_FILE" ]; then
        echo "Error: config file not found: $CONFIG_FILE" >&2
        exit 1
    fi
}

get_json() {
    python3 -c "import sys,json; d=json.load(open('$CONFIG_FILE')); print($1)" 2>/dev/null \
        || jq -r "$2" "$CONFIG_FILE" 2>/dev/null
}

get_wan_ip() {
    local wan_iface
    wan_iface=$(get_json "d['server']['wan_interface']" '.server.wan_interface')
    ip -4 addr show "$wan_iface" | grep -oP 'inet \K[0-9.]+'
}

do_up() {
    read_config
    local remote_endpoint local_ipv6 remote_ipv6 ttl mtu wan_ip lan_enabled

    remote_endpoint=$(get_json "d['tunnel']['remote_endpoint']" '.tunnel.remote_endpoint')
    local_ipv6=$(get_json "d['tunnel']['local_ipv6']" '.tunnel.local_ipv6')
    ttl=$(get_json "d['tunnel']['ttl']" '.tunnel.ttl')
    mtu=$(get_json "d['tunnel']['mtu']" '.tunnel.mtu')
    wan_ip=$(get_wan_ip)
    lan_enabled=$(get_json "d['lan']['enabled']" '.lan.enabled')

    log_message "bringing tunnel up: remote=$remote_endpoint local=$wan_ip"

    # Load kernel module
    modprobe sit 2>/dev/null || true

    # Route to broker endpoint via WAN (bypass VPN PBR)
    local wan_iface
    wan_iface=$(get_json "d['server']['wan_interface']" '.server.wan_interface')
    ip route add "${remote_endpoint}/32" dev "$wan_iface" src "$wan_ip" 2>/dev/null || true

    # Create tunnel
    ip tunnel add "$TUNNEL_IFACE" mode sit \
        remote "$remote_endpoint" \
        local "$wan_ip" \
        ttl "$ttl"

    ip link set "$TUNNEL_IFACE" mtu "$mtu"
    ip link set "$TUNNEL_IFACE" up

    # Assign tunnel IPv6 address
    ip -6 addr add "$local_ipv6" dev "$TUNNEL_IFACE"

    # Default IPv6 route via tunnel
    ip -6 route add ::/0 dev "$TUNNEL_IFACE"

    # Enable IPv6 forwarding
    sysctl -w net.ipv6.conf.all.forwarding=1 > /dev/null

    # Assign prefixes to LAN interfaces
    if [ "$lan_enabled" = "true" ] || [ "$lan_enabled" = "True" ]; then
        local count
        count=$(get_json "len(d['lan']['networks'])" '.lan.networks | length')
        for i in $(seq 0 $((count - 1))); do
            local iface prefix
            iface=$(get_json "d['lan']['networks'][$i]['interface']" ".lan.networks[$i].interface")
            prefix=$(get_json "d['lan']['networks'][$i]['prefix']" ".lan.networks[$i].prefix")
            # Extract prefix address part (e.g., 2001:db8:1:: from 2001:db8:1::/64)
            local prefix_addr
            prefix_addr=$(echo "$prefix" | sed 's|/.*||')
            local prefix_len
            prefix_len=$(echo "$prefix" | sed 's|.*/||')
            ip -6 addr add "${prefix_addr}1/${prefix_len}" dev "$iface" 2>/dev/null || true
            log_message "assigned ${prefix_addr}1/${prefix_len} to $iface"
        done
    fi

    # Firewall rules (nftables)
    nft add table ip filter 2>/dev/null || true
    nft add chain ip filter ipv6tunnel-input '{ type filter hook input priority 0; }' 2>/dev/null || true
    nft add rule ip filter ipv6tunnel-input iifname "$wan_iface" ip protocol 41 accept 2>/dev/null || true

    nft add table ip6 filter 2>/dev/null || true
    nft add chain ip6 filter ipv6tunnel-forward '{ type filter hook forward priority 0; }' 2>/dev/null || true
    nft add rule ip6 filter ipv6tunnel-forward iifname "$TUNNEL_IFACE" ct state established,related accept 2>/dev/null || true
    nft add rule ip6 filter ipv6tunnel-forward oifname "$TUNNEL_IFACE" accept 2>/dev/null || true
    nft add rule ip6 filter ipv6tunnel-forward iifname "$TUNNEL_IFACE" ct state new drop 2>/dev/null || true

    log_message "tunnel up"
}

do_down() {
    log_message "bringing tunnel down"

    # Remove firewall rules
    nft delete chain ip filter ipv6tunnel-input 2>/dev/null || true
    nft delete chain ip6 filter ipv6tunnel-forward 2>/dev/null || true

    # Remove LAN prefixes
    if [ -f "$CONFIG_FILE" ]; then
        local lan_enabled
        lan_enabled=$(get_json "d['lan']['enabled']" '.lan.enabled' 2>/dev/null || echo "false")
        if [ "$lan_enabled" = "true" ] || [ "$lan_enabled" = "True" ]; then
            local count
            count=$(get_json "len(d['lan']['networks'])" '.lan.networks | length' 2>/dev/null || echo "0")
            for i in $(seq 0 $((count - 1))); do
                local iface prefix
                iface=$(get_json "d['lan']['networks'][$i]['interface']" ".lan.networks[$i].interface")
                prefix=$(get_json "d['lan']['networks'][$i]['prefix']" ".lan.networks[$i].prefix")
                local prefix_addr prefix_len
                prefix_addr=$(echo "$prefix" | sed 's|/.*||')
                prefix_len=$(echo "$prefix" | sed 's|.*/||')
                ip -6 addr del "${prefix_addr}1/${prefix_len}" dev "$iface" 2>/dev/null || true
            done
        fi
    fi

    # Remove default IPv6 route
    ip -6 route del ::/0 dev "$TUNNEL_IFACE" 2>/dev/null || true

    # Remove tunnel
    ip tunnel del "$TUNNEL_IFACE" 2>/dev/null || true

    # Remove explicit route to broker
    if [ -f "$CONFIG_FILE" ]; then
        local remote_endpoint wan_iface
        remote_endpoint=$(get_json "d['tunnel']['remote_endpoint']" '.tunnel.remote_endpoint' 2>/dev/null || echo "")
        wan_iface=$(get_json "d['server']['wan_interface']" '.server.wan_interface' 2>/dev/null || echo "ppp0")
        if [ -n "$remote_endpoint" ]; then
            ip route del "${remote_endpoint}/32" dev "$wan_iface" 2>/dev/null || true
        fi
    fi

    log_message "tunnel down"
}

do_restart() {
    do_down
    sleep 1
    do_up
}

do_status() {
    local tunnel_up="false"
    local wan_ip=""
    local local_ipv6=""
    local ping_ok="false"
    local ping_ms=0

    if ip link show "$TUNNEL_IFACE" &>/dev/null; then
        tunnel_up="true"
    fi

    if [ -f "$CONFIG_FILE" ]; then
        wan_ip=$(get_wan_ip 2>/dev/null || echo "")
        local_ipv6=$(get_json "d['tunnel']['local_ipv6']" '.tunnel.local_ipv6' 2>/dev/null || echo "")
    fi

    # Ping check
    if [ "$tunnel_up" = "true" ] && [ -f "$CONFIG_FILE" ]; then
        local target
        target=$(get_json "d['health']['target']" '.health.target' 2>/dev/null || echo "2001:4860:4860::8888")
        local ping_out
        if ping_out=$(ping -6 -c 1 -W 3 "$target" 2>/dev/null); then
            ping_ok="true"
            ping_ms=$(echo "$ping_out" | grep -oP 'time=\K[0-9.]+' | head -1 | cut -d. -f1)
            [ -z "$ping_ms" ] && ping_ms=0
        fi
    fi

    # Build networks JSON
    local networks_json="[]"
    if [ -f "$CONFIG_FILE" ]; then
        local lan_enabled
        lan_enabled=$(get_json "d['lan']['enabled']" '.lan.enabled' 2>/dev/null || echo "false")
        if [ "$lan_enabled" = "true" ] || [ "$lan_enabled" = "True" ]; then
            local count
            count=$(get_json "len(d['lan']['networks'])" '.lan.networks | length' 2>/dev/null || echo "0")
            networks_json="["
            for i in $(seq 0 $((count - 1))); do
                local iface prefix
                iface=$(get_json "d['lan']['networks'][$i]['interface']" ".lan.networks[$i].interface")
                prefix=$(get_json "d['lan']['networks'][$i]['prefix']" ".lan.networks[$i].prefix")
                [ "$i" -gt 0 ] && networks_json="${networks_json},"
                networks_json="${networks_json}{\"interface\":\"${iface}\",\"prefix\":\"${prefix}\"}"
            done
            networks_json="${networks_json}]"
        fi
    fi

    cat <<STATUSEOF
{"tunnel_up":${tunnel_up},"interface":"${TUNNEL_IFACE}","local_ipv6":"${local_ipv6}","wan_ipv4":"${wan_ip}","networks":${networks_json},"ping_ok":${ping_ok},"ping_ms":${ping_ms}}
STATUSEOF
}

do_boot() {
    log_message "boot: starting tunnel and server"
    do_up
    if [ -x "$SERVER_BIN" ]; then
        "$SERVER_BIN" &
        log_message "server started (pid $!)"
    else
        log_message "server binary not found: $SERVER_BIN"
    fi
}

# Main
case "${1:-}" in
    up)      do_up ;;
    down)    do_down ;;
    restart) do_restart ;;
    status)  do_status ;;
    boot)    do_boot ;;
    *)
        echo "Usage: $0 {up|down|restart|status|boot}" >&2
        exit 1
        ;;
esac
```

- [ ] **Step 2: Make executable**

```bash
chmod +x scripts/tunnel.sh
```

- [ ] **Step 3: Commit**

```bash
git add scripts/tunnel.sh
git commit -m "feat: add tunnel.sh for 6in4 tunnel management"
```

---

### Task 8: Web UI

**Files:**
- Create: `web/index.html`

- [ ] **Step 1: Create the SPA**

Create `web/index.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>IPv6 Tunnel Manager</title>
<style>
  :root {
    --bg: #1a1a2e;
    --bg2: #16213e;
    --bg3: #0f3460;
    --accent: #00b4d8;
    --accent-hover: #0096c7;
    --text: #e0e0e0;
    --text-dim: #8892a4;
    --green: #4caf50;
    --red: #f44336;
    --border: #2a2a4a;
    --input-bg: #1a1a3e;
  }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg);
    color: var(--text);
    min-height: 100vh;
    padding: 20px;
  }
  .container { max-width: 700px; margin: 0 auto; }
  header {
    display: flex; justify-content: space-between; align-items: center;
    padding: 16px 0; border-bottom: 1px solid var(--border); margin-bottom: 24px;
  }
  header h1 { font-size: 20px; font-weight: 600; }
  .status-card {
    background: var(--bg2); border-radius: 12px; padding: 24px;
    margin-bottom: 16px; border: 1px solid var(--border);
  }
  .status-row {
    display: flex; justify-content: space-between; align-items: center;
    padding: 8px 0; border-bottom: 1px solid var(--border);
  }
  .status-row:last-child { border-bottom: none; }
  .status-label { color: var(--text-dim); font-size: 14px; }
  .status-value { font-family: monospace; font-size: 14px; }
  .badge {
    display: inline-block; padding: 2px 10px; border-radius: 12px;
    font-size: 12px; font-weight: 600;
  }
  .badge-up { background: rgba(76,175,80,0.2); color: var(--green); }
  .badge-down { background: rgba(244,67,54,0.2); color: var(--red); }
  .controls {
    display: flex; gap: 8px; margin-bottom: 16px;
  }
  button {
    padding: 10px 20px; border: none; border-radius: 8px; cursor: pointer;
    font-size: 14px; font-weight: 500; transition: background 0.2s;
  }
  .btn-start { background: var(--green); color: white; }
  .btn-start:hover { background: #45a049; }
  .btn-stop { background: var(--red); color: white; }
  .btn-stop:hover { background: #e53935; }
  .btn-restart { background: var(--accent); color: white; }
  .btn-restart:hover { background: var(--accent-hover); }
  button:disabled { opacity: 0.5; cursor: not-allowed; }
  .settings {
    background: var(--bg2); border-radius: 12px; padding: 24px;
    border: 1px solid var(--border);
  }
  .settings summary {
    cursor: pointer; font-size: 16px; font-weight: 600;
    padding: 4px 0; list-style: none;
  }
  .settings summary::before { content: "\2699\FE0F  "; }
  .form-group { margin-top: 16px; }
  .form-group label {
    display: block; font-size: 13px; color: var(--text-dim);
    margin-bottom: 4px;
  }
  .form-group input, .form-group select {
    width: 100%; padding: 8px 12px; background: var(--input-bg);
    border: 1px solid var(--border); border-radius: 6px;
    color: var(--text); font-family: monospace; font-size: 14px;
  }
  .form-group input:focus, .form-group select:focus {
    outline: none; border-color: var(--accent);
  }
  .form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  .section-title {
    font-size: 14px; font-weight: 600; color: var(--accent);
    margin-top: 20px; margin-bottom: 8px;
    padding-top: 12px; border-top: 1px solid var(--border);
  }
  .networks-list { margin-top: 8px; }
  .network-entry {
    display: grid; grid-template-columns: 100px 1fr 80px 40px;
    gap: 8px; align-items: center; margin-bottom: 8px;
  }
  .network-entry input { font-size: 13px; }
  .btn-sm {
    padding: 6px 12px; font-size: 12px; border-radius: 6px;
  }
  .btn-add { background: var(--bg3); color: var(--accent); border: 1px solid var(--accent); }
  .btn-del { background: transparent; color: var(--red); border: none; font-size: 18px; cursor: pointer; }
  .btn-save {
    margin-top: 20px; width: 100%;
    background: var(--accent); color: white; padding: 12px;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .checkbox-group { display: flex; align-items: center; gap: 8px; margin-top: 16px; }
  .checkbox-group input[type="checkbox"] { width: auto; }
  .toast {
    position: fixed; bottom: 20px; right: 20px; padding: 12px 20px;
    border-radius: 8px; font-size: 14px; display: none; z-index: 100;
  }
  .toast-ok { background: var(--green); color: white; }
  .toast-err { background: var(--red); color: white; }
</style>
</head>
<body>
<div class="container">
  <header>
    <h1>IPv6 Tunnel Manager</h1>
    <span id="refresh-indicator" style="color:var(--text-dim);font-size:12px"></span>
  </header>

  <div class="status-card" id="status-card">
    <div class="status-row">
      <span class="status-label">Status</span>
      <span id="st-badge" class="badge badge-down">DOWN</span>
    </div>
    <div class="status-row">
      <span class="status-label">Interface</span>
      <span class="status-value" id="st-iface">-</span>
    </div>
    <div class="status-row">
      <span class="status-label">WAN IPv4</span>
      <span class="status-value" id="st-wan">-</span>
    </div>
    <div class="status-row">
      <span class="status-label">Local IPv6</span>
      <span class="status-value" id="st-ipv6">-</span>
    </div>
    <div class="status-row">
      <span class="status-label">Ping</span>
      <span class="status-value" id="st-ping">-</span>
    </div>
    <div id="st-networks"></div>
  </div>

  <div class="controls">
    <button class="btn-start" onclick="tunnelAction('up')">Start</button>
    <button class="btn-stop" onclick="tunnelAction('down')">Stop</button>
    <button class="btn-restart" onclick="tunnelAction('restart')">Restart</button>
  </div>

  <details class="settings">
    <summary>Settings</summary>
    <form id="config-form" onsubmit="saveConfig(event)">
      <div class="section-title">Tunnel</div>
      <div class="form-group">
        <label>Broker</label>
        <select id="cfg-broker">
          <option value="he">Hurricane Electric</option>
          <option value="ip4market">ip4market.ru</option>
          <option value="6in4ru">6in4.ru</option>
          <option value="custom">Custom</option>
        </select>
      </div>
      <div class="form-group">
        <label>Remote Endpoint (IPv4)</label>
        <input id="cfg-remote-endpoint" placeholder="216.66.88.98">
      </div>
      <div class="form-row">
        <div class="form-group">
          <label>Local IPv6</label>
          <input id="cfg-local-ipv6" placeholder="2001:470:..::2/64">
        </div>
        <div class="form-group">
          <label>Remote IPv6</label>
          <input id="cfg-remote-ipv6" placeholder="2001:470:..::1/64">
        </div>
      </div>
      <div class="form-row">
        <div class="form-group">
          <label>TTL</label>
          <input id="cfg-ttl" type="number" value="255">
        </div>
        <div class="form-group">
          <label>MTU</label>
          <input id="cfg-mtu" type="number" value="1480">
        </div>
      </div>

      <div class="section-title">LAN</div>
      <div class="checkbox-group">
        <input type="checkbox" id="cfg-lan-enabled">
        <label for="cfg-lan-enabled">Enable IPv6 on LAN</label>
      </div>
      <div class="form-row">
        <div class="form-group">
          <label>DNS Servers</label>
          <input id="cfg-dns" placeholder="2606:4700:4700::1111, 2001:4860:4860::8888">
        </div>
        <div class="form-group">
          <label>Mode</label>
          <select id="cfg-mode">
            <option value="slaac">SLAAC</option>
            <option value="dhcpv6">DHCPv6</option>
          </select>
        </div>
      </div>
      <div class="form-group">
        <label>Networks</label>
        <div id="networks-list" class="networks-list"></div>
        <button type="button" class="btn-sm btn-add" onclick="addNetwork()">+ Add Network</button>
      </div>

      <div class="section-title">Health Check</div>
      <div class="checkbox-group">
        <input type="checkbox" id="cfg-health-enabled">
        <label for="cfg-health-enabled">Enable health monitoring</label>
      </div>
      <div class="form-row">
        <div class="form-group">
          <label>Interval (sec)</label>
          <input id="cfg-health-interval" type="number" value="30">
        </div>
        <div class="form-group">
          <label>Ping Target</label>
          <input id="cfg-health-target" placeholder="2001:4860:4860::8888">
        </div>
      </div>
      <div class="checkbox-group">
        <input type="checkbox" id="cfg-auto-restart">
        <label for="cfg-auto-restart">Auto-restart on failure</label>
      </div>

      <div class="section-title">Server</div>
      <div class="form-row">
        <div class="form-group">
          <label>Port</label>
          <input id="cfg-port" type="number" value="8686">
        </div>
        <div class="form-group">
          <label>WAN Interface</label>
          <input id="cfg-wan-iface" value="ppp0">
        </div>
      </div>
      <div class="form-group">
        <label>Auth Token</label>
        <input id="cfg-token" type="password" placeholder="(leave empty to disable)">
      </div>

      <button type="submit" class="btn-save">Save & Restart Tunnel</button>
    </form>
  </details>
</div>

<div id="toast" class="toast"></div>

<script>
const API = window.location.origin;
let authToken = localStorage.getItem('ipv6tunnel_token') || '';

function headers() {
  const h = {'Content-Type': 'application/json'};
  if (authToken) h['Authorization'] = 'Bearer ' + authToken;
  return h;
}

async function api(method, path, body) {
  const opts = {method, headers: headers()};
  if (body) opts.body = JSON.stringify(body);
  const res = await fetch(API + path, opts);
  if (res.status === 401) {
    authToken = prompt('Enter auth token:') || '';
    localStorage.setItem('ipv6tunnel_token', authToken);
    return api(method, path, body);
  }
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function toast(msg, ok) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast ' + (ok ? 'toast-ok' : 'toast-err');
  el.style.display = 'block';
  setTimeout(() => el.style.display = 'none', 3000);
}

async function refreshStatus() {
  try {
    const st = await api('GET', '/api/status');
    const badge = document.getElementById('st-badge');
    badge.textContent = st.tunnel_up ? 'UP' : 'DOWN';
    badge.className = 'badge ' + (st.tunnel_up ? 'badge-up' : 'badge-down');
    document.getElementById('st-iface').textContent = st.interface || '-';
    document.getElementById('st-wan').textContent = st.wan_ipv4 || '-';
    document.getElementById('st-ipv6').textContent = st.local_ipv6 || '-';
    if (st.ping_ok) {
      document.getElementById('st-ping').innerHTML =
        '<span style="color:var(--green)">' + st.ping_ms + 'ms</span>';
    } else {
      document.getElementById('st-ping').innerHTML =
        st.tunnel_up ? '<span style="color:var(--red)">failed</span>' : '-';
    }
    // Networks
    const nw = document.getElementById('st-networks');
    if (st.networks && st.networks.length > 0) {
      nw.innerHTML = st.networks.map(n =>
        '<div class="status-row"><span class="status-label">' + n.interface +
        '</span><span class="status-value">' + n.prefix + '</span></div>'
      ).join('');
    } else {
      nw.innerHTML = '';
    }
  } catch (e) {
    console.error('status:', e);
  }
}

async function tunnelAction(action) {
  document.querySelectorAll('.controls button').forEach(b => b.disabled = true);
  try {
    await api('POST', '/api/tunnel/' + action);
    toast('Tunnel ' + action + ' OK', true);
    setTimeout(refreshStatus, 1000);
  } catch (e) {
    toast('Error: ' + e.message, false);
  } finally {
    document.querySelectorAll('.controls button').forEach(b => b.disabled = false);
  }
}

function addNetwork(iface, prefix, comment) {
  const list = document.getElementById('networks-list');
  const row = document.createElement('div');
  row.className = 'network-entry';
  row.innerHTML =
    '<input placeholder="br0" value="' + (iface||'') + '">' +
    '<input placeholder="2001:470:xxxx:1::/64" value="' + (prefix||'') + '">' +
    '<input placeholder="Name" value="' + (comment||'') + '">' +
    '<span class="btn-del" onclick="this.parentElement.remove()">x</span>';
  list.appendChild(row);
}

async function loadConfig() {
  try {
    const cfg = await api('GET', '/api/config');
    document.getElementById('cfg-broker').value = cfg.tunnel.broker || 'he';
    document.getElementById('cfg-remote-endpoint').value = cfg.tunnel.remote_endpoint || '';
    document.getElementById('cfg-local-ipv6').value = cfg.tunnel.local_ipv6 || '';
    document.getElementById('cfg-remote-ipv6').value = cfg.tunnel.remote_ipv6 || '';
    document.getElementById('cfg-ttl').value = cfg.tunnel.ttl || 255;
    document.getElementById('cfg-mtu').value = cfg.tunnel.mtu || 1480;
    document.getElementById('cfg-lan-enabled').checked = cfg.lan.enabled;
    document.getElementById('cfg-dns').value = (cfg.lan.dns || []).join(', ');
    document.getElementById('cfg-mode').value = cfg.lan.mode || 'slaac';
    document.getElementById('networks-list').innerHTML = '';
    (cfg.lan.networks || []).forEach(n => addNetwork(n.interface, n.prefix, n.comment));
    document.getElementById('cfg-health-enabled').checked = cfg.health.enabled;
    document.getElementById('cfg-health-interval').value = cfg.health.interval_sec || 30;
    document.getElementById('cfg-health-target').value = cfg.health.target || '';
    document.getElementById('cfg-auto-restart').checked = cfg.health.auto_restart;
    document.getElementById('cfg-port').value = cfg.server.port || 8686;
    document.getElementById('cfg-wan-iface').value = cfg.server.wan_interface || 'ppp0';
    document.getElementById('cfg-token').value = cfg.server.auth_token || '';
  } catch (e) {
    console.error('load config:', e);
  }
}

async function saveConfig(e) {
  e.preventDefault();
  const networks = [];
  document.querySelectorAll('.network-entry').forEach(row => {
    const inputs = row.querySelectorAll('input');
    if (inputs[0].value) {
      networks.push({
        interface: inputs[0].value,
        prefix: inputs[1].value,
        comment: inputs[2].value
      });
    }
  });
  const cfg = {
    tunnel: {
      broker: document.getElementById('cfg-broker').value,
      remote_endpoint: document.getElementById('cfg-remote-endpoint').value,
      local_ipv6: document.getElementById('cfg-local-ipv6').value,
      remote_ipv6: document.getElementById('cfg-remote-ipv6').value,
      ttl: parseInt(document.getElementById('cfg-ttl').value),
      mtu: parseInt(document.getElementById('cfg-mtu').value)
    },
    lan: {
      enabled: document.getElementById('cfg-lan-enabled').checked,
      dns: document.getElementById('cfg-dns').value.split(',').map(s => s.trim()).filter(Boolean),
      mode: document.getElementById('cfg-mode').value,
      networks: networks
    },
    health: {
      enabled: document.getElementById('cfg-health-enabled').checked,
      interval_sec: parseInt(document.getElementById('cfg-health-interval').value),
      target: document.getElementById('cfg-health-target').value,
      auto_restart: document.getElementById('cfg-auto-restart').checked
    },
    server: {
      port: parseInt(document.getElementById('cfg-port').value),
      wan_interface: document.getElementById('cfg-wan-iface').value,
      auth_token: document.getElementById('cfg-token').value
    }
  };
  try {
    await api('PUT', '/api/config', cfg);
    toast('Config saved & tunnel restarted', true);
    // Update stored token if changed
    if (cfg.server.auth_token !== authToken) {
      authToken = cfg.server.auth_token;
      localStorage.setItem('ipv6tunnel_token', authToken);
    }
    setTimeout(refreshStatus, 2000);
  } catch (e) {
    toast('Error: ' + e.message, false);
  }
}

// Init
refreshStatus();
loadConfig();
setInterval(refreshStatus, 5000);
</script>
</body>
</html>
```

- [ ] **Step 2: Commit**

```bash
git add web/index.html
git commit -m "feat: add web UI dashboard for tunnel management"
```

---

### Task 9: Main Entry Point

**Files:**
- Create: `web.go` (root of project — embed source)
- Create: `cmd/server/main.go`

- [ ] **Step 1: Create web.go for embed at project root**

Create `web.go` (at project root):

```go
package main

import "embed"

//go:embed web
var WebContent embed.FS
```

Note: `go:embed` cannot use `../` paths, so the embed must live at the project root where `web/` directory is. However, since `cmd/server/main.go` is `package main` too, we need a different approach. The simplest: embed directly in `cmd/server/` by symlinking or by moving the embed.

**Better approach:** Move the embed into a dedicated package.

Create `internal/web/embed.go`:

```go
package web

import "embed"

// Content holds the embedded web UI files.
// The go:generate directive copies web/ to the right place at build time.
// Instead, we use -ldflags or a build-time copy.
```

Actually, the **simplest correct approach** for Go embed: put the HTML into `cmd/server/` and embed from there.

Delete the `web.go` step above. Instead:

- [ ] **Step 1: Move web content for embed**

The `web/index.html` file stays at project root for development. At build time, Makefile copies it into `cmd/server/web/` for embedding. Update Makefile:

Add to `Makefile` before the `build` target:

```makefile
build:
	mkdir -p cmd/server/web
	cp web/index.html cmd/server/web/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
		-ldflags="-s -w" \
		-o bin/$(BINARY_NAME) ./cmd/server/
```

Add to `clean` target:

```makefile
clean:
	rm -rf bin/ cmd/server/web/
```

Add `cmd/server/web/` to `.gitignore`:

```gitignore
bin/
cmd/server/web/
*.exe
.DS_Store
```

- [ ] **Step 2: Create main.go**

Create `cmd/server/main.go`:

```go
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

//go:embed web
var webContent embed.FS

type configStore struct {
	mu   sync.RWMutex
	cfg  *config.Config
	path string
}

func (s *configStore) Get() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *configStore) Update(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.Save(s.path, cfg); err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

func main() {
	configPath := flag.String("config", "", "path to config.json")
	scriptPath := flag.String("script", "", "path to tunnel.sh")
	flag.Parse()

	baseDir := filepath.Dir(os.Args[0])
	if *configPath == "" {
		*configPath = filepath.Join(baseDir, "config.json")
	}
	if *scriptPath == "" {
		*scriptPath = filepath.Join(baseDir, "tunnel.sh")
	}

	// Load or create config
	cfg, err := config.Load(*configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = config.Defaults()
			if err := config.Save(*configPath, cfg); err != nil {
				log.Fatalf("create default config: %v", err)
			}
			log.Printf("created default config at %s", *configPath)
		} else {
			log.Fatalf("load config: %v", err)
		}
	}

	store := &configStore{cfg: cfg, path: *configPath}
	mgr := tunnel.NewManager(*scriptPath)
	handler := api.NewHandler(mgr, store)

	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}
	srv := api.NewServer(handler, cfg.Server.AuthToken, webFS)

	// Start RA advertiser if LAN enabled
	if cfg.LAN.Enabled && len(cfg.LAN.Networks) > 0 {
		startRA(cfg)
	}

	// Start health checker
	if cfg.Health.Enabled {
		go healthLoop(mgr, cfg)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port)
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv))
}

func startRA(cfg *config.Config) {
	var networks []ra.NetworkEntry
	for _, nw := range cfg.LAN.Networks {
		prefix, err := netip.ParsePrefix(nw.Prefix)
		if err != nil {
			log.Printf("ra: invalid prefix %q: %v", nw.Prefix, err)
			continue
		}
		networks = append(networks, ra.NetworkEntry{
			Interface: nw.Interface,
			Prefix:    prefix,
		})
	}
	var dns []netip.Addr
	for _, d := range cfg.LAN.DNS {
		addr, err := netip.ParseAddr(d)
		if err != nil {
			log.Printf("ra: invalid dns %q: %v", d, err)
			continue
		}
		dns = append(dns, addr)
	}
	if len(networks) > 0 {
		_, err := ra.NewAdvertiser(ra.AdvertiserConfig{
			Networks: networks,
			DNS:      dns,
			Interval: 10 * time.Second,
		})
		if err != nil {
			log.Printf("ra: failed to start advertiser: %v", err)
		} else {
			log.Printf("ra: advertising on %d interfaces", len(networks))
		}
	}
}

func healthLoop(mgr *tunnel.Manager, cfg *config.Config) {
	interval := time.Duration(cfg.Health.IntervalSec) * time.Second
	if interval < 5*time.Second {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		st, err := mgr.Status()
		if err != nil {
			log.Printf("health: status error: %v", err)
			continue
		}
		if st.TunnelUp && !st.PingOK && cfg.Health.AutoRestart {
			log.Printf("health: ping failed, restarting tunnel")
			if err := mgr.Restart(); err != nil {
				log.Printf("health: restart error: %v", err)
			}
		}
	}
}
```

- [ ] **Step 2: Verify build compiles**

```bash
go mod tidy
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/ipv6-tunnel-server ./cmd/server/
```

Expected: builds without errors, produces `bin/ipv6-tunnel-server`

- [ ] **Step 3: Run all tests**

```bash
go test ./... -v
```

Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go go.mod go.sum
git commit -m "feat: add main entry point wiring config, tunnel, RA, and API"
```

---

### Task 10: Install & Deployment Scripts

**Files:**
- Create: `scripts/install.sh`
- Create: `scripts/rc-local-fragment.sh`

- [ ] **Step 1: Create rc-local-fragment.sh**

Create `scripts/rc-local-fragment.sh`:

```bash
# --- ipv6-tunnel ---
/data/ipv6-tunnel/tunnel.sh boot
# --- /ipv6-tunnel ---
```

- [ ] **Step 2: Create install.sh**

Create `scripts/install.sh`:

```bash
#!/bin/bash
set -eu

INSTALL_DIR="/data/ipv6-tunnel"
RC_LOCAL="/etc/rc.local"
MARKER_START="# --- ipv6-tunnel ---"
MARKER_END="# --- /ipv6-tunnel ---"

log_message() {
    echo "[install] $*"
}

log_message "installing ipv6-tunnel to ${INSTALL_DIR}"

# Create directory structure
mkdir -p "${INSTALL_DIR}/web"

# Copy files from /tmp (uploaded by Makefile)
cp /tmp/ipv6-tunnel-server "${INSTALL_DIR}/"
cp /tmp/tunnel.sh "${INSTALL_DIR}/"
cp /tmp/rc-local-fragment.sh "${INSTALL_DIR}/"
cp /tmp/index.html "${INSTALL_DIR}/web/"

chmod +x "${INSTALL_DIR}/ipv6-tunnel-server"
chmod +x "${INSTALL_DIR}/tunnel.sh"

# Generate default config if not exists
if [ ! -f "${INSTALL_DIR}/config.json" ]; then
    log_message "generating default config.json"
    cat > "${INSTALL_DIR}/config.json" << 'CFGEOF'
{
  "tunnel": {
    "broker": "he",
    "remote_endpoint": "",
    "local_ipv6": "",
    "remote_ipv6": "",
    "ttl": 255,
    "mtu": 1480
  },
  "lan": {
    "enabled": false,
    "dns": ["2606:4700:4700::1111", "2001:4860:4860::8888"],
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
    "port": 8686,
    "wan_interface": "ppp0",
    "auth_token": ""
  }
}
CFGEOF
fi

# Add to rc.local (if not already present)
if ! grep -q "$MARKER_START" "$RC_LOCAL" 2>/dev/null; then
    log_message "adding boot entry to ${RC_LOCAL}"
    # Insert before the final 'exit 0' if it exists, otherwise append
    if grep -q "^exit 0" "$RC_LOCAL"; then
        sed -i "/^exit 0/i\\
${MARKER_START}\\
${INSTALL_DIR}/tunnel.sh boot\\
${MARKER_END}" "$RC_LOCAL"
    else
        cat >> "$RC_LOCAL" << RCEOF

${MARKER_START}
${INSTALL_DIR}/tunnel.sh boot
${MARKER_END}
RCEOF
    fi
else
    log_message "boot entry already present in ${RC_LOCAL}"
fi

# Stop existing server if running
pkill -f ipv6-tunnel-server 2>/dev/null || true
sleep 1

# Start server
log_message "starting server"
"${INSTALL_DIR}/ipv6-tunnel-server" &

log_message "installation complete"
log_message "access UI at http://$(hostname -I | awk '{print $1}'):8686/"

# Clean up temp files
rm -f /tmp/ipv6-tunnel-server /tmp/tunnel.sh /tmp/rc-local-fragment.sh /tmp/index.html
```

- [ ] **Step 3: Make scripts executable**

```bash
chmod +x scripts/install.sh scripts/rc-local-fragment.sh
```

- [ ] **Step 4: Commit**

```bash
git add scripts/install.sh scripts/rc-local-fragment.sh
git commit -m "feat: add install.sh and rc-local-fragment.sh for router deployment"
```

---

### Task 11: Final Build Verification & Cleanup

**Files:**
- Modify: `go.mod` (tidy)

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -v
```

Expected: all tests pass

- [ ] **Step 2: Build for target**

```bash
make build
```

Expected: `bin/ipv6-tunnel-server` exists, is a static ARM64 binary

- [ ] **Step 3: Verify binary is static ARM64**

```bash
file bin/ipv6-tunnel-server
```

Expected: `ELF 64-bit LSB executable, ARM aarch64, ... statically linked`

- [ ] **Step 4: Check binary size**

```bash
ls -lh bin/ipv6-tunnel-server
```

Expected: ~5-10 MB

- [ ] **Step 5: Final commit**

```bash
go mod tidy
git add -A
git commit -m "chore: tidy modules and finalize project"
```
