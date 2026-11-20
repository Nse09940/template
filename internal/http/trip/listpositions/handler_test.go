package listpositions_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/trip/listpositions"
	"github.com/Nse09940/template/internal/model"
)

func TestListTripPositions(t *testing.T) {
	t.Parallel()
	tripID := uuid.New()
	at := time.Date(2026, time.November, 20, 12, 34, 56, 0, time.UTC)
	for _, tt := range []struct {
		name      string
		err       error
		positions []model.Position
		status    int
	}{
		{name: "empty", status: http.StatusOK},
		{
			name:   "route",
			status: http.StatusOK,
			positions: []model.Position{
				{ID: 4, TripID: tripID, RecordedAt: at},
				{ID: 5, TripID: tripID, RecordedAt: at},
				{ID: 1, TripID: tripID, RecordedAt: at.Add(time.Minute)},
			},
		},
		{name: "not found", err: trip.ErrTripNotFound, status: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := NewMockusecase(gomock.NewController(t))
			u.EXPECT().List(gomock.Any(), tripID).Return(tt.positions, tt.err)
			handler := listpositions.New(u, slog.New(slog.DiscardHandler))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/trips/"+tripID.String()+"/positions", nil)
			res := httptest.NewRecorder()
			handler.ListTripPositions(res, req, tripID)
			if res.Code != tt.status {
				t.Fatalf("status=%d body=%s", res.Code, res.Body)
			}
			if tt.err != nil {
				var body api.Problem
				if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Code != "trip_not_found" {
					t.Fatalf("problem=%+v", body)
				}
				return
			}
			var body api.TripPositionsResponse
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Positions == nil || len(body.Positions) != len(tt.positions) {
				t.Fatalf("positions=%+v", body.Positions)
			}
			for index, point := range body.Positions {
				expected := tt.positions[index]
				if point.Id != expected.ID || point.TripId != tripID || !point.RecordedAt.Equal(expected.RecordedAt) {
					t.Fatalf("position[%d]=%+v", index, point)
				}
			}
		})
	}
}
