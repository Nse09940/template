package trip

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
)

func (r *Repository) UpdateLastPositionAt(ctx context.Context, tripID uuid.UUID, recordedAt time.Time) error {
	const query = `
		UPDATE trips
		SET last_position_at = GREATEST(last_position_at, $2), updated_at = now()
		WHERE id = $1 AND status = 'active'`

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	result, err := r.executor(ctx).Exec(queryCtx, query, tripID, recordedAt)
	if err != nil {
		return fmt.Errorf("execute last-position update: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	if _, err := r.GetByID(queryCtx, tripID); err != nil {
		return err
	}
	return trip.ErrTripCompleted
}

func (r *Repository) InsertPosition(ctx context.Context, value model.Position) (model.Position, error) {
	const query = `
		INSERT INTO trip_positions (trip_id, latitude, longitude, recorded_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, recorded_at`

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	err := r.executor(ctx).QueryRow(queryCtx, query, value.TripID, value.Coordinates.Latitude,
		value.Coordinates.Longitude, value.RecordedAt).Scan(&value.ID, &value.RecordedAt)
	if err != nil {
		return model.Position{}, fmt.Errorf("scan inserted trip position: %w", err)
	}
	value.RecordedAt = value.RecordedAt.UTC()
	return value, nil
}

func (r *Repository) ListPositions(ctx context.Context, tripID uuid.UUID) ([]model.Position, error) {
	const query = `
		SELECT id, latitude, longitude, recorded_at
		FROM trip_positions WHERE trip_id = $1
		ORDER BY recorded_at, id`

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	rows, err := r.executor(ctx).Query(queryCtx, query, tripID)
	if err != nil {
		return nil, fmt.Errorf("query trip positions: %w", err)
	}
	positions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Position, error) {
		value := model.Position{TripID: tripID}
		err := row.Scan(&value.ID, &value.Coordinates.Latitude, &value.Coordinates.Longitude, &value.RecordedAt)
		value.RecordedAt = value.RecordedAt.UTC()
		return value, err
	})
	if err != nil {
		return nil, fmt.Errorf("read trip positions: %w", err)
	}
	return positions, nil
}

func (r *Repository) FindStaleTrips(ctx context.Context, staleAfter time.Duration) ([]model.StaleTrip, error) {
	const query = `
		SELECT id, driver_id
		FROM trips
		WHERE status = 'active'
		  AND COALESCE(last_position_at, started_at) < CURRENT_TIMESTAMP - $1::interval
		ORDER BY COALESCE(last_position_at, started_at), id`

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	threshold := pgtype.Interval{Microseconds: staleAfter.Microseconds(), Valid: true}
	rows, err := r.executor(ctx).Query(queryCtx, query, threshold)
	if err != nil {
		return nil, fmt.Errorf("query stale trips: %w", err)
	}
	trips, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.StaleTrip, error) {
		var candidate model.StaleTrip
		err := row.Scan(&candidate.TripID, &candidate.DriverID)
		return candidate, err
	})
	if err != nil {
		return nil, fmt.Errorf("read stale trips: %w", err)
	}
	return trips, nil
}
