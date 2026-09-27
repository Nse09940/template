package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/Nse09940/template/internal/trip"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const activeDriverConstraint = "trips_one_active_per_driver_idx"

var statementBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

type dbExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repository struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func NewRepository(pool *pgxpool.Pool, queryTimeout time.Duration) *Repository {
	return &Repository{pool: pool, queryTimeout: queryTimeout}
}

func (r *Repository) Insert(ctx context.Context, value trip.Trip) error {
	query, args, err := statementBuilder.
		Insert("trips").
		Columns(
			"id",
			"user_id",
			"driver_id",
			"start_latitude",
			"start_longitude",
			"end_latitude",
			"end_longitude",
			"price",
			"status",
			"started_at",
			"finished_at",
		).
		Values(
			value.ID,
			value.UserID,
			value.DriverID,
			value.StartPoint.Latitude,
			value.StartPoint.Longitude,
			value.EndPoint.Latitude,
			value.EndPoint.Longitude,
			value.Price,
			string(value.Status),
			value.StartedAt,
			value.FinishedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert trip query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	_, err = r.executor(ctx).Exec(queryCtx, query, args...)
	if err != nil {
		var postgresErr *pgconn.PgError
		if errors.As(err, &postgresErr) &&
			postgresErr.Code == "23505" &&
			postgresErr.ConstraintName == activeDriverConstraint {
			return trip.ErrDriverBusy
		}
		return fmt.Errorf("execute insert trip query: %w", err)
	}

	return nil
}

func (r *Repository) InsertStatusHistory(
	ctx context.Context,
	tripID uuid.UUID,
	fromStatus *trip.Status,
	toStatus trip.Status,
	reason string,
	changedAt time.Time,
) error {
	var fromValue any
	if fromStatus != nil {
		fromValue = string(*fromStatus)
	}

	query, args, err := statementBuilder.
		Insert("trip_status_history").
		Columns("trip_id", "from_status", "to_status", "reason", "changed_at").
		Values(tripID, fromValue, string(toStatus), reason, changedAt).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert status history query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	if _, err := r.executor(ctx).Exec(queryCtx, query, args...); err != nil {
		return fmt.Errorf("execute insert status history query: %w", err)
	}

	return nil
}

func (r *Repository) DeleteExpiredCreateIdempotency(
	ctx context.Context,
	key uuid.UUID,
	now time.Time,
) error {
	query, args, err := statementBuilder.
		Delete("trip_create_idempotency_keys").
		Where(sq.Eq{"idempotency_key": key}).
		Where(sq.LtOrEq{"expires_at": now}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build delete expired idempotency key query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	if _, err := r.executor(ctx).Exec(queryCtx, query, args...); err != nil {
		return fmt.Errorf("execute delete expired idempotency key query: %w", err)
	}

	return nil
}

func (r *Repository) TryReserveCreateIdempotency(
	ctx context.Context,
	key uuid.UUID,
	requestHash []byte,
	tripID uuid.UUID,
	createdAt time.Time,
	expiresAt time.Time,
) (bool, error) {
	query, args, err := statementBuilder.
		Insert("trip_create_idempotency_keys").
		Columns(
			"idempotency_key",
			"request_hash",
			"trip_id",
			"created_at",
			"expires_at",
		).
		Values(key, requestHash, tripID, createdAt, expiresAt).
		Suffix("ON CONFLICT (idempotency_key) DO NOTHING").
		ToSql()
	if err != nil {
		return false, fmt.Errorf("build reserve idempotency key query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	commandTag, err := r.executor(ctx).Exec(queryCtx, query, args...)
	if err != nil {
		return false, fmt.Errorf("execute reserve idempotency key query: %w", err)
	}

	return commandTag.RowsAffected() == 1, nil
}

func (r *Repository) GetCreateIdempotency(
	ctx context.Context,
	key uuid.UUID,
) (trip.CreateIdempotency, error) {
	query, args, err := statementBuilder.
		Select("request_hash", "trip_id").
		From("trip_create_idempotency_keys").
		Where(sq.Eq{"idempotency_key": key}).
		ToSql()
	if err != nil {
		return trip.CreateIdempotency{}, fmt.Errorf("build get idempotency key query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	var stored trip.CreateIdempotency
	if err := r.executor(ctx).QueryRow(queryCtx, query, args...).Scan(
		&stored.RequestHash,
		&stored.TripID,
	); err != nil {
		return trip.CreateIdempotency{}, fmt.Errorf("scan idempotency key row: %w", err)
	}

	return stored, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	query, args, err := statementBuilder.
		Select(tripColumns()...).
		From("trips").
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return trip.Trip{}, fmt.Errorf("build get trip query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	found, err := scanTrip(r.executor(ctx).QueryRow(queryCtx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return trip.Trip{}, trip.ErrNotFound
	}
	if err != nil {
		return trip.Trip{}, fmt.Errorf("scan get trip row: %w", err)
	}

	return found, nil
}

func (r *Repository) FinishActive(
	ctx context.Context,
	id uuid.UUID,
	finishedAt time.Time,
) (trip.Trip, error) {
	query, args, err := statementBuilder.
		Update("trips").
		Set("status", string(trip.StatusCompleted)).
		Set("finished_at", finishedAt).
		Set("updated_at", finishedAt).
		Where(sq.Eq{
			"id":     id,
			"status": string(trip.StatusActive),
		}).
		Suffix("RETURNING " + strings.Join(tripColumns(), ", ")).
		ToSql()
	if err != nil {
		return trip.Trip{}, fmt.Errorf("build finish trip query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	finished, err := scanTrip(r.executor(ctx).QueryRow(queryCtx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return trip.Trip{}, trip.ErrNotFound
	}
	if err != nil {
		return trip.Trip{}, fmt.Errorf("scan finished trip row: %w", err)
	}

	return finished, nil
}

func (r *Repository) executor(ctx context.Context) dbExecutor {
	if tx := transactionFromContext(ctx); tx != nil {
		return tx
	}
	return r.pool
}

func tripColumns() []string {
	return []string{
		"id",
		"user_id",
		"driver_id",
		"start_latitude",
		"start_longitude",
		"end_latitude",
		"end_longitude",
		"price",
		"status",
		"started_at",
		"finished_at",
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrip(row rowScanner) (trip.Trip, error) {
	var value trip.Trip
	var status string
	err := row.Scan(
		&value.ID,
		&value.UserID,
		&value.DriverID,
		&value.StartPoint.Latitude,
		&value.StartPoint.Longitude,
		&value.EndPoint.Latitude,
		&value.EndPoint.Longitude,
		&value.Price,
		&status,
		&value.StartedAt,
		&value.FinishedAt,
	)
	value.Status = trip.Status(status)
	return value, err
}
