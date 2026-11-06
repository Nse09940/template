package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/requeststate"
	"github.com/Nse09940/template/internal/http/respond"
	"github.com/Nse09940/template/internal/http/trip/create"
	"github.com/Nse09940/template/internal/http/trip/finish"
	"github.com/Nse09940/template/internal/http/trip/get"
)

type Handler struct {
	*create.Handler
	GetHandler    *get.Handler
	FinishHandler *finish.Handler
	database      pinger
	timeout       time.Duration
	response      respond.Writer
}

func NewHandler(c *create.Handler, g *get.Handler, f *finish.Handler, db pinger, timeout time.Duration, logger *slog.Logger) *Handler {
	return &Handler{Handler: c, GetHandler: g, FinishHandler: f, database: db, timeout: timeout, response: respond.Writer{Logger: logger}}
}

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	h.GetHandler.GetTrip(w, r, id)
}

func (h *Handler) FinishTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	h.FinishHandler.FinishTrip(w, r, id)
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.response.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	if err := h.database.Ping(ctx); err != nil {
		requeststate.SetError(r.Context(), fmt.Errorf("readiness database ping: %w", err))
		h.response.JSON(w, r, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	h.response.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (h *Handler) ParameterError(w http.ResponseWriter, r *http.Request, err error) {
	h.response.Error(w, r, &trip.ValidationError{Message: err.Error()})
}
