//go:build integration

package trip_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Nse09940/template/internal/infra/postgres/transaction"
	"github.com/Nse09940/template/internal/model"
	"github.com/Nse09940/template/internal/usecase/trip/createposition"
)

func TestFindStaleTripsFiltersAndOrdersByLastSeen(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	const staleAfter = time.Minute
	fixtures := []struct {
		name        string
		startedAgo  time.Duration
		positionAgo time.Duration
		completed   bool
	}{
		{name: "old trip with a stale position", startedAgo: 20 * time.Minute, positionAgo: 2 * time.Minute},
		{name: "old trip with a fresh position", startedAgo: 10 * time.Minute, positionAgo: 10 * time.Second},
		{name: "old trip without positions", startedAgo: 3 * time.Minute},
		{name: "completed trip", startedAgo: 15 * time.Minute, completed: true},
		{name: "fresh trip without positions", startedAgo: 10 * time.Second},
		{name: "trip exactly at the threshold", startedAgo: staleAfter},
	}
	trips := make([]model.StaleTrip, len(fixtures))
	err := db.tx.Do(t.Context(), func(ctx context.Context) error {
		// PostgreSQL uses the transaction's clock for both the fixtures and the selector.
		var now time.Time
		if err := transaction.FromContext(ctx).QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&now); err != nil {
			return fmt.Errorf("read database clock: %w", err)
		}
		for i, fixture := range fixtures {
			value := activeTrip()
			value.StartedAt = now.Add(-fixture.startedAgo)
			if err := db.repo.Insert(ctx, value); err != nil {
				return fmt.Errorf("insert %s: %w", fixture.name, err)
			}
			if fixture.positionAgo != 0 {
				if err := db.repo.UpdateLastPositionAt(ctx, value.ID, now.Add(-fixture.positionAgo)); err != nil {
					return fmt.Errorf("add position to %s: %w", fixture.name, err)
				}
			}
			if fixture.completed {
				if _, err := db.repo.FinishActive(ctx, value.ID, now.Add(-time.Minute)); err != nil {
					return fmt.Errorf("finish %s: %w", fixture.name, err)
				}
			}
			trips[i] = model.StaleTrip{TripID: value.ID, DriverID: value.DriverID}
		}
		got, err := db.repo.FindStaleTrips(ctx, staleAfter)
		if err != nil {
			return err
		}
		want := []model.StaleTrip{trips[2], trips[0]}
		if !slices.Equal(got, want) {
			return fmt.Errorf("stale trips = %+v, want %+v", got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type positionObserver struct {
	saved     int
	cancelled int
}

func (o *positionObserver) PositionSaved(context.Context) { o.saved++ }
func (o *positionObserver) Cancel(uuid.UUID)              { o.cancelled++ }

func TestSavePositionCommitsAndRollsBackAtomically(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	value := activeTrip()
	ctx := t.Context()
	if err := db.repo.Insert(ctx, value); err != nil {
		t.Fatalf("insert active trip: %v", err)
	}
	observer := &positionObserver{}
	usecase := createposition.New(db.repo, db.tx, observer, observer, func() time.Time {
		return value.StartedAt.Add(5 * time.Minute)
	})
	latest := value.StartedAt.Add(time.Minute)
	position := model.Position{
		TripID:      value.ID,
		Coordinates: model.Coordinates{Latitude: 59.9, Longitude: 30.3},
	}
	persisted := make([]model.Position, 3)
	for i, at := range []time.Time{latest, value.StartedAt, value.StartedAt} {
		position.RecordedAt = at
		stored, err := usecase.Save(ctx, position)
		if err != nil {
			t.Fatalf("save position %d: %v", i, err)
		}
		if stored.ID <= 0 || stored.TripID != value.ID || stored.Coordinates != position.Coordinates || !stored.RecordedAt.Equal(at) {
			t.Fatalf("saved position = %+v, input %+v", stored, position)
		}
		persisted[i] = stored
	}
	assertLastPosition(t, db, value.ID, latest)
	want := []model.Position{persisted[1], persisted[2], persisted[0]}
	assertRoute(t, db, value.ID, want)

	// A valid request reaches the second write, where PostgreSQL rejects it.
	if _, err := db.pool.Exec(ctx, `ALTER TABLE trip_positions
		ADD CONSTRAINT reject_test_position CHECK (latitude <> 42)`); err != nil {
		t.Fatalf("install test constraint: %v", err)
	}
	position.RecordedAt = latest.Add(time.Minute)
	position.Coordinates.Latitude = 42
	_, err := usecase.Save(ctx, position)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.ConstraintName != "reject_test_position" {
		t.Fatalf("save rejected position = %v, want check constraint error", err)
	}
	assertLastPosition(t, db, value.ID, latest)
	assertRoute(t, db, value.ID, want)
	if observer.saved != len(persisted) || observer.cancelled != len(persisted) {
		t.Fatalf("saved/cancelled observers = %d/%d, want %d each", observer.saved, observer.cancelled, len(persisted))
	}
}

func assertLastPosition(t *testing.T, db database, tripID uuid.UUID, want time.Time) {
	t.Helper()
	stored, err := db.repo.GetByID(t.Context(), tripID)
	if err != nil {
		t.Fatalf("load trip: %v", err)
	}
	if stored.LastPositionAt == nil || !stored.LastPositionAt.Equal(want) {
		t.Fatalf("last_position_at = %v, want %s", stored.LastPositionAt, want)
	}
}

func assertRoute(t *testing.T, db database, tripID uuid.UUID, want []model.Position) {
	t.Helper()
	got, err := db.repo.ListPositions(t.Context(), tripID)
	if err != nil {
		t.Fatalf("list trip positions: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("route = %+v, want %+v", got, want)
	}
}
