package listpositions_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
	"github.com/Nse09940/template/internal/usecase/trip/listpositions"
)

func TestListPositions(t *testing.T) {
	t.Parallel()
	tripID := uuid.New()
	positions := []model.Position{{ID: 42, TripID: tripID}}
	storageFailure := errors.New("storage failure")
	for _, tt := range []struct {
		name                     string
		getErr, listErr, wantErr error
	}{
		{name: "completed trip route"},
		{name: "trip missing", getErr: trip.ErrTripNotFound, wantErr: trip.ErrTripNotFound},
		{name: "list failure", listErr: storageFailure, wantErr: storageFailure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := NewMocktripRepository(gomock.NewController(t))
			ctx := t.Context()
			get := repo.EXPECT().GetByID(ctx, tripID).
				Return(model.Trip{ID: tripID, Status: trip.StatusCompleted}, tt.getErr)
			if tt.getErr == nil {
				repo.EXPECT().ListPositions(ctx, tripID).After(get).Return(positions, tt.listErr)
			}
			response, err := listpositions.New(repo).List(ctx, tripID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error=%v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && !slices.Equal(response, positions) {
				t.Fatalf("route=%+v, want %+v", response, positions)
			}
		})
	}
}
