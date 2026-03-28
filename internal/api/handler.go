package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

// Controller abstracts daemon control-plane operations for HTTP handlers.
type Controller interface {
	Status() (*tunnel.Status, error)
	Health() (*tunnel.Status, error)
	Up() error
	Down() error
	Restart() error
	GetConfig() *config.Config
	UpdateConfig(cfg *config.Config) error
}

// Handler holds the HTTP handler dependencies.
type Handler struct {
	controller Controller
}

// NewHandler creates a Handler with the given controller.
func NewHandler(controller Controller) *Handler {
	return &Handler{controller: controller}
}

func (h *Handler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.controller.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) HandleGetHealth(w http.ResponseWriter, r *http.Request) {
	status, err := h.controller.Health()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) handleTunnelCommand(w http.ResponseWriter, fn func() error) {
	if err := fn(); err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) HandleTunnelUp(w http.ResponseWriter, r *http.Request) {
	h.handleTunnelCommand(w, h.controller.Up)
}

func (h *Handler) HandleTunnelDown(w http.ResponseWriter, r *http.Request) {
	h.handleTunnelCommand(w, h.controller.Down)
}

func (h *Handler) HandleTunnelRestart(w http.ResponseWriter, r *http.Request) {
	h.handleTunnelCommand(w, h.controller.Restart)
}

func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.controller.GetConfig())
}

func (h *Handler) HandleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := config.ParseUpdate(body, h.controller.GetConfig())
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.controller.UpdateConfig(cfg); err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeControlError(w http.ResponseWriter, err error) {
	var validationErr *config.ValidationError
	if errors.As(err, &validationErr) {
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
