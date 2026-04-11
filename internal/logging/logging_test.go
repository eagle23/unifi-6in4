package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesLogFileAndTailReadsEntries(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	defer Close()
	slog.Info("first message", "key", "value")
	slog.Warn("second message")
	lines, err := Tail(10)
	if err != nil {
		t.Fatalf("Tail() error: %v", err)
	}
	if len(lines) < 2 {
		t.Fatalf("Tail() returned %d lines, want >= 2", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "first message") {
		t.Fatalf("Tail() did not contain first message: %s", joined)
	}
	if !strings.Contains(joined, "second message") {
		t.Fatalf("Tail() did not contain second message: %s", joined)
	}
}

func TestInitRotatesOversizedLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LogFileName)
	filler := make([]byte, MaxLogFileBytes+1)
	for i := range filler {
		filler[i] = 'x'
	}
	if err := os.WriteFile(path, filler, 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := Init(dir); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	defer Close()
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(new log) error: %v", err)
	}
	if info.Size() >= MaxLogFileBytes {
		t.Fatalf("new log size = %d, want < %d", info.Size(), MaxLogFileBytes)
	}
}

func TestTailRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	defer Close()
	for i := 0; i < 50; i++ {
		slog.Info("entry", "index", i)
	}
	lines, err := Tail(5)
	if err != nil {
		t.Fatalf("Tail() error: %v", err)
	}
	if len(lines) != 5 {
		t.Fatalf("Tail(5) returned %d lines, want 5", len(lines))
	}
	// The last entry must be index=49 (the most recent).
	if !strings.Contains(lines[len(lines)-1], `"index":49`) {
		t.Fatalf("last line = %q, want index=49", lines[len(lines)-1])
	}
}

func TestTailReturnsNilWhenNotInitialized(t *testing.T) {
	// Ensure global state is clean.
	_ = Close()
	lines, err := Tail(10)
	if err != nil {
		t.Fatalf("Tail() error: %v", err)
	}
	if lines != nil {
		t.Fatalf("Tail() = %v, want nil", lines)
	}
}
