package listpositions

import (
	"log/slog"
	"net/http"

	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/respond"
	"github.com/Nse09940/template/internal/http/trip/convert"
)

type Handler struct {
	usecase  usecase
	response respond.Writer
}

func New(u usecase, logger *slog.Logger) *Handler {
	return &Handler{usecase: u, response: respond.Writer{Logger: logger}}
}

func (h *Handler) ListTripPositions(w http.ResponseWriter, r *http.Request, id api.TripId) {
	found, err := h.usecase.List(r.Context(), id)
	if err != nil {
		h.response.Error(w, r, err)
		return
	}

	positions := make([]api.TripPosition, 0, len(found))
	for _, position := range found {
		positions = append(positions, convert.Position(position))
	}
	h.response.JSON(w, r, http.StatusOK, api.TripPositionsResponse{Positions: positions})
}
