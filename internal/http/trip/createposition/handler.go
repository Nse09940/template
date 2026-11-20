package createposition

import (
	"log/slog"
	"net/http"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/respond"
	"github.com/Nse09940/template/internal/http/trip/convert"
	"github.com/Nse09940/template/internal/http/trip/request"
	"github.com/Nse09940/template/internal/model"
)

type Handler struct {
	usecase  usecase
	response respond.Writer
}

func New(u usecase, logger *slog.Logger) *Handler {
	return &Handler{usecase: u, response: respond.Writer{Logger: logger}}
}

func (h *Handler) CreateTripPosition(w http.ResponseWriter, r *http.Request, id api.TripId) {
	var body api.PositionData
	if err := request.Decode(w, r, &body, []string{"coordinates", "recorded_at"}, "coordinates"); err != nil {
		h.response.Error(w, r, &trip.ValidationError{Message: err.Error()})
		return
	}

	saved, err := h.usecase.Save(r.Context(), model.Position{
		TripID:      id,
		Coordinates: model.Coordinates(body.Coordinates),
		RecordedAt:  body.RecordedAt,
	})
	if err != nil {
		h.response.Error(w, r, err)
		return
	}
	h.response.JSON(w, r, http.StatusCreated, convert.Position(saved))
}
