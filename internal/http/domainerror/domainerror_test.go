package domainerror_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/http/domainerror"
)

func TestProblem(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{trip.ErrTripNotFound, 404, "trip_not_found"}, {trip.ErrTripCompleted, 409, "trip_completed"}, {trip.ErrDriverBusy, 409, "driver_busy"}, {trip.ErrIdempotencyConflict, 409, "idempotency_conflict"}, {&trip.ValidationError{Field: "price", Message: "must not be negative"}, 400, "invalid_request"}, {errors.New("password=secret SQL failed"), 500, "internal_error"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			t.Parallel()
			status, body := domainerror.Problem(fmt.Errorf("operation failed: %w", tt.err))
			if status != tt.status || int(body.Status) != status || body.Code != tt.code {
				t.Fatalf("status=%d body=%+v", status, body)
			}
			if body.Detail == nil || body.Type == "" {
				t.Fatalf("missing problem fields: %+v", body)
			}
			if strings.Contains(*body.Detail, "secret") {
				t.Fatal("internal detail leaked")
			}
		})
	}
}
