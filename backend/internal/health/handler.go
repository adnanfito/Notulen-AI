// Package health exposes the readiness endpoint the frontend polls.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"notulen-ai/backend/internal/config"
	"notulen-ai/backend/internal/database"
)

type Handler struct {
	cfg    *config.Config
	db     *database.DB
	client *http.Client
}

func NewHandler(cfg *config.Config, db *database.DB) *Handler {
	return &Handler{
		cfg:    cfg,
		db:     db,
		client: &http.Client{Timeout: cfg.STTHealthCheck},
	}
}

type dependency struct {
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Error   string `json:"error,omitempty"`
	Model   string `json:"model,omitempty"`
	Device  string `json:"device,omitempty"`
	Compute string `json:"compute_type,omitempty"`
}

type response struct {
	Status       string                `json:"status"`
	Service      string                `json:"service"`
	Time         string                `json:"time"`
	Dependencies map[string]dependency `json:"dependencies"`
}

// sttHealth mirrors the STT service's /health payload.
type sttHealth struct {
	Status          string `json:"status"`
	Model           string `json:"model"`
	Device          string `json:"device"`
	DeviceRequested string `json:"device_requested"`
	ComputeType     string `json:"compute_type"`
	ModelLoaded     bool   `json:"model_loaded"`
}

// Check reports the backend's own state plus each dependency it needs.
//
// It answers 200 even when a dependency is down, with status "degraded": the
// frontend needs to render *which* piece is broken, and a 503 with no body
// detail would tell it nothing.
func (h *Handler) Check(c *gin.Context) {
	deps := map[string]dependency{
		"database": h.checkDatabase(c.Request.Context()),
		"stt":      h.checkSTT(c.Request.Context()),
	}

	status := "ok"
	for _, d := range deps {
		if d.Status != "ok" {
			status = "degraded"
			break
		}
	}

	c.JSON(http.StatusOK, response{
		Status:       status,
		Service:      "backend",
		Time:         time.Now().UTC().Format(time.RFC3339),
		Dependencies: deps,
	})
}

func (h *Handler) checkDatabase(ctx context.Context) dependency {
	if err := h.db.Health(ctx); err != nil {
		return dependency{Status: "down", Error: err.Error()}
	}
	return dependency{Status: "ok"}
}

func (h *Handler) checkSTT(ctx context.Context) dependency {
	ctx, cancel := context.WithTimeout(ctx, h.cfg.STTHealthCheck)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.STTURL+"/health", nil)
	if err != nil {
		return dependency{Status: "down", Error: err.Error()}
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return dependency{Status: "down", Error: "stt service unreachable"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return dependency{Status: "down", Error: "stt returned " + resp.Status}
	}

	var body sttHealth
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return dependency{Status: "degraded", Error: "unreadable stt health payload"}
	}

	dep := dependency{
		Status:  "ok",
		Model:   body.Model,
		Device:  body.Device,
		Compute: body.ComputeType,
	}
	if !body.ModelLoaded {
		dep.Status = "degraded"
		dep.Detail = "model not loaded"
	}
	// Surface a silent CPU fallback rather than letting it pass as healthy;
	// on a GPU machine this is the difference between realtime and useless.
	if body.DeviceRequested != "" && body.DeviceRequested != body.Device {
		dep.Status = "degraded"
		dep.Detail = "requested " + body.DeviceRequested + ", running on " + body.Device
	}
	return dep
}
