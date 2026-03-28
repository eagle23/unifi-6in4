package api

import (
	"io/fs"
	"net/http"
	"strings"
)

// AuthMiddleware wraps next with Bearer token authentication.
// If token is empty, all requests pass through without checking.
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

// NewServer builds an http.Handler with all API routes, auth, and optional static file serving.
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
