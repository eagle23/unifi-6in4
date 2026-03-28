package webui

import (
	"embed"
	"net/http"
)

var (
	//go:embed index.html
	embeddedFiles embed.FS
)

// FileSystem returns the embedded web UI as an http.FileSystem.
func FileSystem() http.FileSystem {
	return http.FS(embeddedFiles)
}
