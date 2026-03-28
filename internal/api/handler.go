package api

import (
	"encoding/json"
	"net/http"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

// TunnelController abstracts tunnel operations for the HTTP handler.
type TunnelController interface {
	Status() (*tunnel.Status, error)
	Up() error
	Down() error
	Restart() error
}

// ConfigStore abstracts config read/write for the HTTP handler.
type ConfigStore interface {
	Get() *config.Config
	Update(cfg *config.Config) error
}

// Handler holds the HTTP handler dependencies.
type Handler struct {
	tunnel TunnelController
	config ConfigStore
}

// NewHandler creates a Handler with the given tunnel controller and config store.
func NewHandler(tc TunnelController, cs ConfigStore) *Handler {
	return &Handler{tunnel: tc, config: cs}
}

// HandleGetStatus writes the current tunnel status as JSON.
func (h *Handler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.tunnel.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// HandleTunnelUp brings the tunnel up and responds with {"status":"ok"}.
func (h *Handler) HandleTunnelUp(w http.ResponseWriter, r *http.Request) {
	if err := h.tunnel.Up(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleTunnelDown brings the tunnel down and responds with {"status":"ok"}.
func (h *Handler) HandleTunnelDown(w http.ResponseWriter, r *http.Request) {
	if err := h.tunnel.Down(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleTunnelRestart restarts the tunnel and responds with {"status":"ok"}.
func (h *Handler) HandleTunnelRestart(w http.ResponseWriter, r *http.Request) {
	if err := h.tunnel.Restart(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleGetConfig writes the current config as JSON.
func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.config.Get())
}

// HandleUpdateConfig decodes a Config from the request body, saves it, and restarts the tunnel.
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
