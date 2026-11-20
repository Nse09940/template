package domainerror

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
)

func Problem(err error) (int, api.Problem) {
	status, code, title, detail := http.StatusInternalServerError, "internal_error", "Internal server error", "An internal error occurred"
	var validation *trip.ValidationError
	switch {
	case errors.As(err, &validation):
		status, code, title, detail = http.StatusBadRequest, "invalid_request", "Invalid request", validation.Error()
	case errors.Is(err, trip.ErrTripNotFound):
		status, code, title, detail = http.StatusNotFound, "trip_not_found", "Trip not found", "Trip does not exist"
	case errors.Is(err, trip.ErrTripCompleted):
		status, code, title, detail = http.StatusConflict, "trip_completed", "Trip completed", "Trip is already completed"
	case errors.Is(err, trip.ErrDriverBusy):
		status, code, title, detail = http.StatusConflict, "driver_busy", "Driver busy", "Driver already has an active trip"
	case errors.Is(err, trip.ErrIdempotencyConflict):
		status, code, title, detail = http.StatusConflict, "idempotency_conflict", "Idempotency conflict", "Idempotency-Key was already used with a different request body"
	}
	return status, api.Problem{Status: int32(status), Code: code, Title: title, Detail: &detail, Type: "https://tripgo.example/problems/" + strings.ReplaceAll(code, "_", "-")}
}
