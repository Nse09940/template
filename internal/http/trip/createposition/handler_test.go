package createposition_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/trip/createposition"
	"github.com/Nse09940/template/internal/model"
)

const (
	validBody   = `{"coordinates":{"latitude":0,"longitude":0},"recorded_at":"2026-11-20T12:34:56Z"}`
	contentJSON = "application/json"
	problemJSON = "application/problem+json"
)

func TestCreateTripPosition(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		err  error
		code string
		want int
	}{
		{name: "created", want: http.StatusCreated},
		{name: "not found", err: trip.ErrTripNotFound, want: http.StatusNotFound, code: "trip_not_found"},
		{name: "completed", err: trip.ErrTripCompleted, want: http.StatusConflict, code: "trip_completed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tripID := uuid.New()
			at := time.Date(2026, time.November, 20, 12, 34, 56, 0, time.UTC)
			input := model.Position{TripID: tripID, RecordedAt: at}
			saved := input
			saved.ID = 42
			u := NewMockusecase(gomock.NewController(t))
			u.EXPECT().Save(gomock.Any(), input).Return(saved, tt.err)
			handler := createposition.New(u, slog.New(slog.DiscardHandler))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/positions", strings.NewReader(validBody))
			req.Header.Set("Content-Type", contentJSON)
			res := httptest.NewRecorder()
			handler.CreateTripPosition(res, req, tripID)
			if res.Code != tt.want {
				t.Fatalf("status=%d body=%s", res.Code, res.Body)
			}
			if tt.err != nil {
				var body api.Problem
				if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Code != tt.code || res.Header().Get("Content-Type") != problemJSON {
					t.Fatalf("problem=%+v", body)
				}
				return
			}
			var body api.TripPosition
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Id != saved.ID || body.TripId != tripID || !body.RecordedAt.Equal(at) || body.Coordinates != (api.Coordinates{}) {
				t.Fatalf("position=%+v", body)
			}
		})
	}
}

func TestCreateTripPositionRejectsInvalidPayload(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, body string }{
		{"no coordinates", `{"recorded_at":"2026-11-20T12:34:56Z"}`},
		{"no longitude", `{"coordinates":{"latitude":0},"recorded_at":"2026-11-20T12:34:56Z"}`},
		{"null latitude", `{"coordinates":{"latitude":null,"longitude":0},"recorded_at":"2026-11-20T12:34:56Z"}`},
		{"no timestamp", `{"coordinates":{"latitude":0,"longitude":0}}`},
		{"null timestamp", `{"coordinates":{"latitude":0,"longitude":0},"recorded_at":null}`},
		{"malformed timestamp", `{"coordinates":{"latitude":0,"longitude":0},"recorded_at":"tomorrow"}`},
		{"unknown coordinate field", `{"coordinates":{"latitude":0,"longitude":0,"altitude":0},"recorded_at":"2026-11-20T12:34:56Z"}`},
		{"trailing JSON", validBody + " {}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tripID := uuid.New()
			u := NewMockusecase(gomock.NewController(t))
			handler := createposition.New(u, slog.New(slog.DiscardHandler))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/positions", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", contentJSON)
			res := httptest.NewRecorder()
			handler.CreateTripPosition(res, req, tripID)
			if res.Code != http.StatusBadRequest || res.Header().Get("Content-Type") != problemJSON {
				t.Fatalf("status=%d body=%s", res.Code, res.Body)
			}
			var body api.Problem
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "invalid_request" {
				t.Fatalf("problem=%+v", body)
			}
		})
	}
}
