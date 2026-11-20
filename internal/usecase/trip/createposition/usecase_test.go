package createposition_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
	"github.com/Nse09940/template/internal/usecase/trip/createposition"
)

func TestSaveRejectsInvalidPosition(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		field       string
		at          time.Time
		coordinates model.Coordinates
	}{
		{field: "coordinates.latitude", at: now, coordinates: model.Coordinates{Latitude: -91}},
		{field: "coordinates.longitude", at: now, coordinates: model.Coordinates{Longitude: 181}},
		{field: "recorded_at", at: now.Add(time.Minute + time.Nanosecond)},
	} {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			usecase := createposition.New(NewMocktripRepository(ctrl), NewMocktxManager(ctrl),
				NewMockpendingRequests(ctrl), NewMockobserver(ctrl), func() time.Time { return now })
			_, err := usecase.Save(t.Context(), model.Position{
				TripID: uuid.New(), Coordinates: tt.coordinates, RecordedAt: tt.at,
			})
			var validation *trip.ValidationError
			if !errors.As(err, &validation) || validation.Field != tt.field {
				t.Fatalf("error=%v, want validation for %s", err, tt.field)
			}
		})
	}
}

func TestSavePositionTransaction(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 20, 12, 0, 0, 900, time.UTC)
	tripID := uuid.New()
	input := model.Position{
		TripID: tripID, Coordinates: model.Coordinates{Latitude: 90, Longitude: -180},
		RecordedAt: now.Add(time.Minute).In(time.FixedZone("local", 3*60*60)),
	}
	expected := input
	expected.RecordedAt = input.RecordedAt.UTC().Truncate(time.Microsecond)
	saved := expected
	saved.ID = 42
	storageFailure := errors.New("storage failure")
	for _, tt := range []struct {
		name                            string
		updateErr, insertErr, commitErr error
		wantErr                         error
	}{
		{name: "saved"},
		{name: "not found", updateErr: trip.ErrTripNotFound, wantErr: trip.ErrTripNotFound},
		{name: "completed", updateErr: trip.ErrTripCompleted, wantErr: trip.ErrTripCompleted},
		{name: "insert failed", insertErr: storageFailure, wantErr: storageFailure},
		{name: "commit failed", commitErr: storageFailure, wantErr: storageFailure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			manager := NewMocktxManager(ctrl)
			pending := NewMockpendingRequests(ctrl)
			observer := NewMockobserver(ctrl)
			ctx := t.Context()
			txCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			committed := false
			manager.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, within func(context.Context) error) error {
				if err := within(txCtx); err != nil {
					return err
				}
				committed = tt.commitErr == nil
				return tt.commitErr
			})
			update := repo.EXPECT().UpdateLastPositionAt(txCtx, tripID, expected.RecordedAt).Return(tt.updateErr)
			if tt.updateErr == nil {
				repo.EXPECT().InsertPosition(txCtx, expected).After(update).Return(saved, tt.insertErr)
			}
			if tt.wantErr == nil {
				pending.EXPECT().Cancel(tripID).Do(func(uuid.UUID) {
					if !committed {
						t.Error("cancelled before commit")
					}
				})
				observer.EXPECT().PositionSaved(ctx).Do(func(context.Context) {
					if !committed {
						t.Error("observed save before commit")
					}
				})
			}
			usecase := createposition.New(repo, manager, pending, observer, func() time.Time { return now })
			result, err := usecase.Save(ctx, input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error=%v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && result != saved {
				t.Fatalf("result=%+v, want %+v", result, saved)
			}
		})
	}
}
