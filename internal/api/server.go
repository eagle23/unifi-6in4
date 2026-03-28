package api

import (
	"net/http"
	"strings"
)

// TokenFunc returns the current auth token. Called on every request so config changes take effect.
type TokenFunc func() string

func authMiddleware(getToken TokenFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := getToken()
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

func NewServer(h *Handler, getToken TokenFunc, webFS http.FileSystem) http.Handler {
	mux := http.NewServeMux()
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/status", h.HandleGetStatus)
	apiMux.HandleFunc("GET /api/config", h.HandleGetConfig)
	apiMux.HandleFunc("PUT /api/config", h.HandleUpdateConfig)
	apiMux.HandleFunc("GET /api/health", h.HandleGetStatus)
	apiMux.HandleFunc("POST /api/tunnel/up", h.HandleTunnelUp)
	apiMux.HandleFunc("POST /api/tunnel/down", h.HandleTunnelDown)
	apiMux.HandleFunc("POST /api/tunnel/restart", h.HandleTunnelRestart)
	mux.Handle("/api/", authMiddleware(getToken, apiMux))
	if webFS != nil {
		mux.Handle("/", http.FileServer(webFS))
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
