package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScriptCreatesExecutableRcLocalWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	installDir := filepath.Join(tempDir, "data", "ipv6-tunnel")
	rcLocalPath := filepath.Join(tempDir, "etc", "rc.local")
	uploadDir := filepath.Join(tempDir, "upload")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(uploadDir) error: %v", err)
	}
	writeExecutableFile(t, filepath.Join(uploadDir, "ipv6-tunnel-server"), "#!/bin/sh\nexit 0\n")
	writeExecutableFile(t, filepath.Join(uploadDir, "tunnel.sh"), "#!/bin/sh\nexit 0\n")
	writeExecutableFile(t, filepath.Join(uploadDir, "rc-local-fragment.sh"), "# --- ipv6-tunnel ---\n/data/ipv6-tunnel/tunnel.sh boot\n# --- /ipv6-tunnel ---\n")
	command := exec.Command("bash", "scripts/install.sh")
	command.Dir = filepath.Join("..")
	command.Env = append(os.Environ(),
		"IPV6_TUNNEL_INSTALL_DIR="+installDir,
		"IPV6_TUNNEL_RC_LOCAL="+rcLocalPath,
		"IPV6_TUNNEL_TEMP_DIR="+uploadDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh error: %v\n%s", err, string(output))
	}
	rcLocalContent, err := os.ReadFile(rcLocalPath)
	if err != nil {
		t.Fatalf("ReadFile(rcLocalPath) error: %v", err)
	}
	actualContent := string(rcLocalContent)
	if !strings.HasPrefix(actualContent, "#!/bin/sh") {
		t.Fatalf("rc.local missing shebang:\n%s", actualContent)
	}
	if !strings.Contains(actualContent, installDir+"/tunnel.sh boot") {
		t.Fatalf("rc.local missing boot command:\n%s", actualContent)
	}
	if !strings.Contains(actualContent, "\nexit 0\n") {
		t.Fatalf("rc.local missing exit 0:\n%s", actualContent)
	}
	info, err := os.Stat(rcLocalPath)
	if err != nil {
		t.Fatalf("Stat(rcLocalPath) error: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("rc.local is not executable: mode=%o", info.Mode().Perm())
	}
}

func TestInstallScriptRepairsExistingRcLocalFragmentOnlyFile(t *testing.T) {
	tempDir := t.TempDir()
	installDir := filepath.Join(tempDir, "data", "ipv6-tunnel")
	rcLocalPath := filepath.Join(tempDir, "etc", "rc.local")
	uploadDir := filepath.Join(tempDir, "upload")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(uploadDir) error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(rcLocalPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(rcLocalDir) error: %v", err)
	}
	writeExecutableFile(t, filepath.Join(uploadDir, "ipv6-tunnel-server"), "#!/bin/sh\nexit 0\n")
	writeExecutableFile(t, filepath.Join(uploadDir, "tunnel.sh"), "#!/bin/sh\nexit 0\n")
	writeExecutableFile(t, filepath.Join(uploadDir, "rc-local-fragment.sh"), "# --- ipv6-tunnel ---\n/data/ipv6-tunnel/tunnel.sh boot\n# --- /ipv6-tunnel ---\n")
	if err := os.WriteFile(rcLocalPath, []byte("# --- ipv6-tunnel ---\n/data/ipv6-tunnel/tunnel.sh boot\n# --- /ipv6-tunnel ---\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(rcLocalPath) error: %v", err)
	}
	command := exec.Command("bash", "scripts/install.sh")
	command.Dir = filepath.Join("..")
	command.Env = append(os.Environ(),
		"IPV6_TUNNEL_INSTALL_DIR="+installDir,
		"IPV6_TUNNEL_RC_LOCAL="+rcLocalPath,
		"IPV6_TUNNEL_TEMP_DIR="+uploadDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh error: %v\n%s", err, string(output))
	}
	rcLocalContent, err := os.ReadFile(rcLocalPath)
	if err != nil {
		t.Fatalf("ReadFile(rcLocalPath) error: %v", err)
	}
	actualContent := string(rcLocalContent)
	if !strings.HasPrefix(actualContent, "#!/bin/sh") {
		t.Fatalf("rc.local missing shebang:\n%s", actualContent)
	}
	if strings.Count(actualContent, "# --- ipv6-tunnel ---") != 1 {
		t.Fatalf("rc.local duplicated boot fragment:\n%s", actualContent)
	}
	if !strings.Contains(actualContent, "\nexit 0\n") {
		t.Fatalf("rc.local missing exit 0:\n%s", actualContent)
	}
}

func writeExecutableFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", path, err)
	}
}
