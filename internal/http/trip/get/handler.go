package get

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

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	found, err := h.usecase.Get(r.Context(), tripID)
	if err != nil {
		h.response.Error(w, r, err)
		return
	}

	h.response.JSON(w, r, http.StatusOK, convert.Trip(found))
}
