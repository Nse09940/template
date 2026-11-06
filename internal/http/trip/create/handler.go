package create

import (
	"log/slog"
	"net/http"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/respond"
	"github.com/Nse09940/template/internal/http/trip/convert"
	"github.com/Nse09940/template/internal/model"
)

type Handler struct {
	usecase  usecase
	response respond.Writer
}

func New(u usecase, logger *slog.Logger) *Handler {
	return &Handler{usecase: u, response: respond.Writer{Logger: logger}}
}

func (h *Handler) CreateTrip(
	w http.ResponseWriter,
	r *http.Request,
	params api.CreateTripParams,
) {
	if err := requireJSONContentType(r); err != nil {
		h.response.Error(w, r, &trip.ValidationError{Message: err.Error()})
		return
	}

	body, err := decodeTripData(w, r)
	if err != nil {
		h.response.Error(w, r, &trip.ValidationError{Message: err.Error()})
		return
	}

	value := trip.Trip{
		UserID:     body.UserId,
		DriverID:   body.DriverId,
		StartPoint: trip.Coordinates(body.StartPoint),
		EndPoint:   trip.Coordinates(body.EndPoint),
		Price:      body.Price,
	}
	if err := value.Validate(); err != nil {
		h.response.Error(w, r, err)
		return
	}
	result, err := h.usecase.Create(r.Context(), model.CreateInput{
		UserID:         body.UserId,
		DriverID:       body.DriverId,
		IdempotencyKey: params.IdempotencyKey,
		StartPoint: model.Coordinates{
			Latitude:  body.StartPoint.Latitude,
			Longitude: body.StartPoint.Longitude,
		},
		EndPoint: model.Coordinates{
			Latitude:  body.EndPoint.Latitude,
			Longitude: body.EndPoint.Longitude,
		},
		Price: body.Price,
	})
	if err != nil {
		h.response.Error(w, r, err)
		return
	}

	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	} else {
		w.Header().Set("Location", "/api/v1/trips/"+result.Trip.ID.String())
	}
	h.response.JSON(w, r, status, convert.Trip(result.Trip))
}
