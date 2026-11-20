package get_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/trip/get"
	"github.com/Nse09940/template/internal/model"
)

func TestHandler(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"success", nil, 200, ""}, {"missing", trip.ErrTripNotFound, 404, "trip_not_found"}, {"completed", trip.ErrTripCompleted, 409, "trip_completed"}, {"internal", errors.New("dsn password=secret"), 500, "internal_error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := NewMockusecase(gomock.NewController(t))
			id := uuid.New()
			u.EXPECT().Get(gomock.Any(), id).Return(model.Trip{ID: id, Status: trip.StatusActive}, tt.err)
			h := get.New(u, slog.New(slog.NewTextHandler(io.Discard, nil)))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+id.String(), nil)
			res := httptest.NewRecorder()
			h.GetTrip(res, req, id)
			if res.Code != tt.status {
				t.Fatalf("status=%d body=%s", res.Code, res.Body)
			}
			if tt.err == nil {
				var value api.Trip
				if err := json.NewDecoder(res.Body).Decode(&value); err != nil {
					t.Fatal(err)
				}
				if value.Id != id {
					t.Fatal("wrong trip")
				}
				return
			}
			var body api.Problem
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tt.code || strings.Contains(*body.Detail, "secret") {
				t.Fatalf("body=%+v", body)
			}
		})
	}
}
