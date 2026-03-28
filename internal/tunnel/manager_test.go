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
	content := "#!/bin/bash\ncase \"$1\" in\n  status)\n    cat <<'STATUSEOF'\n" + statusJSON + "\nSTATUSEOF\n    ;;\n  up|down|restart)\n    echo \"ok\"\n    ;;\nesac\n"
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
