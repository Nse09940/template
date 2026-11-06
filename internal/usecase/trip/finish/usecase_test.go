package finish_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
	"github.com/Nse09940/template/internal/usecase/trip/finish"
)

func TestFinish(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("database failed")
	for _, tt := range []struct {
		name                                                string
		status                                              trip.Status
		fetchErr, updateErr, historyErr, commitErr, wantErr error
	}{
		{name: "success", status: trip.StatusActive},
		{name: "completed", status: trip.StatusCompleted, wantErr: trip.ErrTripCompleted},
		{name: "missing", fetchErr: trip.ErrTripNotFound, wantErr: trip.ErrTripNotFound},
		{name: "repository failure", fetchErr: dbErr, wantErr: dbErr},
		{name: "concurrent finish", status: trip.StatusActive, updateErr: trip.ErrTripNotFound, wantErr: trip.ErrTripCompleted},
		{name: "update failure", status: trip.StatusActive, updateErr: dbErr, wantErr: dbErr},
		{name: "history failure", status: trip.StatusActive, historyErr: dbErr, wantErr: dbErr},
		{name: "commit failure", status: trip.StatusActive, commitErr: dbErr, wantErr: dbErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			tx := NewMocktxManager(ctrl)
			observer := NewMockobserver(ctrl)
			id := uuid.New()
			ctx := context.Background()
			repo.EXPECT().GetByID(gomock.Any(), id).Return(model.Trip{ID: id, Status: tt.status}, tt.fetchErr)
			tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
				if err := fn(ctx); err != nil {
					return err
				}
				return tt.commitErr
			})
			if tt.fetchErr == nil && tt.status == trip.StatusActive {
				repo.EXPECT().FinishActive(gomock.Any(), id, gomock.Any()).DoAndReturn(func(_ context.Context, _ uuid.UUID, at time.Time) (model.Trip, error) {
					return model.Trip{ID: id, Status: trip.StatusCompleted, FinishedAt: &at}, tt.updateErr
				})
				if tt.updateErr == nil {
					repo.EXPECT().InsertStatusHistory(gomock.Any(), id, gomock.Any(), trip.StatusCompleted, "trip finished", gomock.Any()).Return(tt.historyErr)
				}
			}
			if tt.wantErr == nil {
				observer.EXPECT().Completed(ctx)
			}
			got, err := finish.New(repo, tx, slog.New(slog.NewTextHandler(io.Discard, nil)), observer).Finish(ctx, id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error=%v want=%v", err, tt.wantErr)
			}
			if err == nil && (got.ID != id || got.Status != trip.StatusCompleted || got.FinishedAt == nil) {
				t.Fatalf("result=%+v", got)
			}
		})
	}
}
