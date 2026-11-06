package create_test

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
	"github.com/Nse09940/template/internal/usecase/trip/create"
)

const fetchErrorMode = "fetch error"

const reserveMode = "reserve"

const replayMode = "replay"

const reserveErrorMode = "reserve error"

const lookupErrorMode = "lookup error"

const cleanupErrorMode = "cleanup error"

const conflictMode = "conflict"

type txKey struct{}

func validInput() model.CreateInput {
	return model.CreateInput{UserID: uuid.New(), DriverID: uuid.New(), StartPoint: model.Coordinates{Latitude: 59.9, Longitude: 30.3}, EndPoint: model.Coordinates{Latitude: 59.8, Longitude: 30.4}, Price: 1450}
}
func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func TestCreate(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("database failed")
	for _, tt := range []struct {
		name                             string
		insertErr, historyErr, commitErr error
	}{
		{name: "success"}, {name: "busy", insertErr: trip.ErrDriverBusy}, {name: "insert failure", insertErr: dbErr}, {name: "history failure", historyErr: dbErr}, {name: "commit failure", commitErr: dbErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			tx := NewMocktxManager(ctrl)
			observer := NewMockobserver(ctrl)
			ctx := context.Background()
			txCtx := context.WithValue(ctx, txKey{}, true)
			tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				if err := fn(txCtx); err != nil {
					return err
				}
				return tt.commitErr
			})
			input := validInput()
			var inserted model.Trip
			repo.EXPECT().Insert(txCtx, gomock.Any()).DoAndReturn(func(_ context.Context, v model.Trip) error { inserted = v; return tt.insertErr })
			if tt.insertErr == nil {
				repo.EXPECT().InsertStatusHistory(txCtx, gomock.Any(), nil, trip.StatusActive, "trip created", gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID, _ *trip.Status, _ trip.Status, _ string, at time.Time) error {
					if id != inserted.ID || at != inserted.StartedAt {
						t.Fatal("trip and history do not match")
					}
					return tt.historyErr
				})
			}
			wantErr := errors.Join(tt.insertErr, tt.historyErr, tt.commitErr)
			if wantErr == nil {
				observer.EXPECT().Created(ctx)
			}
			result, err := create.New(repo, tx, logger(), observer).Create(ctx, input)
			if wantErr != nil {
				if err == nil {
					t.Fatal("expected failure")
				}
				cause := tt.insertErr
				if cause == nil {
					cause = tt.historyErr
				}
				if cause == nil {
					cause = tt.commitErr
				}
				if !errors.Is(err, cause) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || result.Replayed || result.Trip.ID == uuid.Nil || result.Trip.UserID != input.UserID || result.Trip.Status != trip.StatusActive || result.Trip.StartedAt.Location() != time.UTC {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestIdempotency(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{reserveMode, replayMode, conflictMode, cleanupErrorMode, reserveErrorMode, lookupErrorMode, fetchErrorMode} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			tx := NewMocktxManager(ctrl)
			observer := NewMockobserver(ctrl)
			input := validInput()
			key := uuid.New()
			input.IdempotencyKey = &key
			ctx := context.Background()
			dbErr := errors.New("database failed")
			tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
			cleanupErr := error(nil)
			if mode == cleanupErrorMode {
				cleanupErr = dbErr
			}
			repo.EXPECT().DeleteExpiredCreateIdempotency(ctx, gomock.Any()).Return(cleanupErr)
			var hash []byte
			existing := model.Trip{ID: uuid.New(), Status: trip.StatusActive}
			if cleanupErr == nil {
				repo.EXPECT().TryReserveCreateIdempotency(ctx, key, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ uuid.UUID, h []byte, id uuid.UUID, at, expires time.Time) (bool, error) {
					if len(h) != 32 || id == uuid.Nil || expires.Sub(at) != 24*time.Hour {
						t.Fatal("invalid reservation")
					}
					hash = append([]byte(nil), h...)
					if mode == reserveErrorMode {
						return false, dbErr
					}
					return mode == reserveMode, nil
				})
				switch mode {
				case reserveMode:
					repo.EXPECT().Insert(ctx, gomock.Any()).Return(nil)
					repo.EXPECT().InsertStatusHistory(ctx, gomock.Any(), nil, trip.StatusActive, "trip created", gomock.Any()).Return(nil)
					observer.EXPECT().Created(ctx)
				case replayMode, conflictMode, lookupErrorMode, fetchErrorMode:
					repo.EXPECT().GetCreateIdempotency(ctx, key).DoAndReturn(func(context.Context, uuid.UUID) (model.CreateIdempotency, error) {
						if mode == lookupErrorMode {
							return model.CreateIdempotency{}, dbErr
						}
						if mode == conflictMode {
							hash = []byte("different")
						}
						return model.CreateIdempotency{RequestHash: hash, TripID: existing.ID}, nil
					})
					if mode == replayMode || mode == fetchErrorMode {
						fetchErr := error(nil)
						if mode == fetchErrorMode {
							fetchErr = dbErr
						}
						repo.EXPECT().GetByID(ctx, existing.ID).Return(existing, fetchErr)
					}
				}
			}
			result, err := create.New(repo, tx, logger(), observer).Create(ctx, input)
			switch mode {
			case reserveMode:
				if err != nil || result.Replayed {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			case replayMode:
				if err != nil || !result.Replayed || result.Trip.ID != existing.ID {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			case conflictMode:
				if !errors.Is(err, trip.ErrIdempotencyConflict) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, dbErr) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestInvalidInputDoesNotStartTransaction(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	_, err := create.New(NewMocktripRepository(ctrl), NewMocktxManager(ctrl), logger(), NewMockobserver(ctrl)).Create(context.Background(), model.CreateInput{})
	var validation *trip.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error=%v", err)
	}
}
