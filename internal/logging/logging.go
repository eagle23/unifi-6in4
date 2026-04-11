// Package logging wires the daemon's global slog logger to a rotating file in
// the runtime data directory. The file survives firmware upgrades because the
// UniFi /data partition is preserved, and is exposed via the HTTP API for the
// web UI.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	MaxLogFileBytes = 10 * 1024 * 1024
	LogFileName     = "daemon.log"
)

var (
	mu          sync.Mutex
	currentPath string
	currentFile *os.File
)

// Init opens <baseDir>/daemon.log, rotates it if oversized, and installs a JSON
// slog handler that writes to both the file and stderr. Safe to call once at
// startup. Subsequent callers replace the global logger.
func Init(baseDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	path := filepath.Join(baseDir, LogFileName)
	if err := rotateIfLarge(path); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if currentFile != nil {
		_ = currentFile.Close()
	}
	currentPath = path
	currentFile = file
	writer := io.MultiWriter(file, os.Stderr)
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	return nil
}

// Path returns the current log file path, or empty string if Init was not called.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return currentPath
}

// Close flushes the underlying log file. Calling it is optional but recommended
// during shutdown to make sure the last records hit disk.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if currentFile == nil {
		return nil
	}
	err := currentFile.Close()
	currentFile = nil
	currentPath = ""
	return err
}

// Tail returns the last n lines of the log file. Empty slice if the file does
// not exist yet. Lines are returned oldest-first so the UI can append them in
// natural reading order.
func Tail(n int) ([]string, error) {
	mu.Lock()
	path := currentPath
	mu.Unlock()
	if path == "" || n <= 0 {
		return nil, nil
	}
	return tailFile(path, n)
}

func rotateIfLarge(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < MaxLogFileBytes {
		return nil
	}
	backup := path + ".1"
	_ = os.Remove(backup)
	return os.Rename(path, backup)
}

func tailFile(path string, n int) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}
	const chunkSize int64 = 8192
	var buffer []byte
	offset := size
	for offset > 0 {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := file.ReadAt(chunk, offset); err != nil {
			return nil, err
		}
		buffer = append(chunk, buffer...)
		if countNewlines(buffer) > n {
			break
		}
	}
	lines := splitLines(buffer)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func countNewlines(data []byte) int {
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	return count
}

func splitLines(data []byte) []string {
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
