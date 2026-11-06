//go:build integration

package trip_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/infra/postgres/transaction"
	"github.com/Nse09940/template/internal/model"
	repository "github.com/Nse09940/template/internal/repository/trip"
	"github.com/Nse09940/template/internal/usecase/trip/create"
	"github.com/Nse09940/template/internal/usecase/trip/finish"
)

type database struct {
	pool     *pgxpool.Pool
	repo     *repository.Repository
	tx       *transaction.TxManager
	logger   *slog.Logger
	observer *businessObserver
}

type businessObserver struct {
	created   atomic.Int64
	completed atomic.Int64
}

func (o *businessObserver) Created(context.Context)   { o.created.Add(1) }
func (o *businessObserver) Completed(context.Context) { o.completed.Add(1) }

func newDatabase(t *testing.T) database {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create administrative pool: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := "integration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	})
	pool := schemaPool(t, ctx, dsn, schema)
	applyMigrations(t, ctx, pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return database{
		pool: pool, repo: repository.NewRepository(pool, 3*time.Second),
		tx:     transaction.NewTxManager(pool, 3*time.Second, logger),
		logger: logger, observer: &businessObserver{},
	}
}

func schemaPool(t *testing.T, ctx context.Context, dsn, schema string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse integration database configuration: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create schema pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	files := os.DirFS("../../..")
	paths, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected the three existing migrations, got %d", len(paths))
	}
	for _, path := range paths {
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		up, _, found := strings.Cut(string(contents), "-- +goose Down")
		if !found {
			t.Fatalf("migration %s lacks a Down marker", path)
		}
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
}

func activeTrip() model.Trip {
	return model.Trip{
		ID: uuid.New(), UserID: uuid.New(), DriverID: uuid.New(),
		StartPoint: model.Coordinates{Latitude: 59.9, Longitude: 30.3},
		EndPoint:   model.Coordinates{Latitude: 59.8, Longitude: 30.4},
		Price:      1450, Status: trip.StatusActive,
		StartedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}

func insertTripAndHistory(ctx context.Context, db database, value model.Trip) error {
	if err := db.repo.Insert(ctx, value); err != nil {
		return err
	}
	return db.repo.InsertStatusHistory(ctx, value.ID, nil, trip.StatusActive, "trip created", value.StartedAt)
}

func assertRowCounts(t *testing.T, pool *pgxpool.Pool, trips, history, keys int) {
	t.Helper()
	var gotTrips, gotHistory, gotKeys int
	err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM trips),
		(SELECT count(*) FROM trip_status_history),
		(SELECT count(*) FROM trip_create_idempotency_keys)`).Scan(&gotTrips, &gotHistory, &gotKeys)
	if err != nil {
		t.Fatalf("count persisted rows: %v", err)
	}
	if gotTrips != trips || gotHistory != history || gotKeys != keys {
		t.Fatalf("rows trips/history/keys = %d/%d/%d, want %d/%d/%d", gotTrips, gotHistory, gotKeys, trips, history, keys)
	}
}

func TestRepositoryCommitsTripAndHistory(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	value := activeTrip()
	err := db.tx.Do(t.Context(), func(ctx context.Context) error {
		return insertTripAndHistory(ctx, db, value)
	})
	if err != nil {
		t.Fatalf("commit trip and history: %v", err)
	}
	found, err := db.repo.GetByID(t.Context(), value.ID)
	if err != nil || !reflect.DeepEqual(found, value) {
		t.Fatalf("roundtrip = %+v, %v, want %+v", found, err, value)
	}
	var from *string
	var to, reason string
	var at time.Time
	err = db.pool.QueryRow(t.Context(),
		"SELECT from_status, to_status, reason, changed_at FROM trip_status_history WHERE trip_id = $1",
		value.ID).Scan(&from, &to, &reason, &at)
	if err != nil || from != nil || to != string(trip.StatusActive) || reason != "trip created" || !at.Equal(value.StartedAt) {
		t.Fatalf("history = %v/%s/%s/%s, error %v", from, to, reason, at, err)
	}
	assertRowCounts(t, db.pool, 1, 1, 0)
}

func TestRepositoryRejectsBusyDriver(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	first := activeTrip()
	if err := db.repo.Insert(t.Context(), first); err != nil {
		t.Fatalf("insert first trip: %v", err)
	}
	second := activeTrip()
	second.DriverID = first.DriverID
	if err := db.repo.Insert(t.Context(), second); !errors.Is(err, trip.ErrDriverBusy) {
		t.Fatalf("insert occupied driver = %v, want ErrDriverBusy", err)
	}
	assertRowCounts(t, db.pool, 1, 0, 0)
}

func TestRepositoryFinishDoesNotOverwriteCompletedTimestamp(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	value := activeTrip()
	if err := db.repo.Insert(t.Context(), value); err != nil {
		t.Fatalf("insert active trip: %v", err)
	}
	at := value.StartedAt.Add(time.Minute)
	finished, err := db.repo.FinishActive(t.Context(), value.ID, at)
	if err != nil || finished.Status != trip.StatusCompleted || finished.FinishedAt == nil || !finished.FinishedAt.Equal(at) {
		t.Fatalf("finish active trip = %+v, %v", finished, err)
	}
	_, err = db.repo.FinishActive(t.Context(), value.ID, at.Add(time.Hour))
	if !errors.Is(err, trip.ErrTripNotFound) {
		t.Fatalf("second conditional update = %v, want ErrTripNotFound", err)
	}
	found, err := db.repo.GetByID(t.Context(), value.ID)
	if err != nil || found.FinishedAt == nil || !found.FinishedAt.Equal(at) {
		t.Fatalf("stored completion time = %v, error %v, want %s", found.FinishedAt, err, at)
	}
}

func TestRepositoryMissingTrip(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	_, err := db.repo.GetByID(t.Context(), uuid.New())
	if !errors.Is(err, trip.ErrTripNotFound) {
		t.Fatalf("fetch missing trip = %v, want ErrTripNotFound", err)
	}
}

func TestTransactionRollsBackBothTablesOnConstraintViolation(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	value := activeTrip()
	err := db.tx.Do(t.Context(), func(ctx context.Context) error {
		if err := insertTripAndHistory(ctx, db, value); err != nil {
			return err
		}
		return db.repo.InsertStatusHistory(ctx, value.ID, nil, trip.Status("invalid"), "invalid transition", value.StartedAt)
	})
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
		t.Fatalf("transaction error = %v, want PostgreSQL CHECK violation", err)
	}
	assertRowCounts(t, db.pool, 0, 0, 0)
}

func TestTransactionRollsBackWithCanceledContext(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := db.tx.Do(ctx, func(txCtx context.Context) error {
		if err := insertTripAndHistory(txCtx, db, activeTrip()); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("transaction error = %v, want cancellation", err)
	}
	assertRowCounts(t, db.pool, 0, 0, 0)
}

func TestTransactionRollsBackAndPropagatesPanic(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		if err := db.tx.Do(t.Context(), func(ctx context.Context) error {
			if err := insertTripAndHistory(ctx, db, activeTrip()); err != nil {
				return err
			}
			panic("rollback marker")
		}); err != nil {
			t.Fatalf("transaction before panic: %v", err)
		}
	}()
	if recovered != "rollback marker" {
		t.Fatalf("recovered = %v, want original panic", recovered)
	}
	assertRowCounts(t, db.pool, 0, 0, 0)
}

type createOutcome struct {
	result model.CreateResult
	err    error
}

func simultaneousCreates(ctx context.Context, usecase *create.Usecase, input model.CreateInput) [2]createOutcome {
	start := make(chan struct{})
	results := make(chan createOutcome, 2)
	for range 2 {
		go func() {
			<-start
			result, err := usecase.Create(ctx, input)
			results <- createOutcome{result: result, err: err}
		}()
	}
	close(start)
	return [2]createOutcome{<-results, <-results}
}

func createInput() model.CreateInput {
	value := activeTrip()
	return model.CreateInput{
		UserID: value.UserID, DriverID: value.DriverID,
		StartPoint: value.StartPoint, EndPoint: value.EndPoint, Price: value.Price,
	}
}

func TestConcurrentCreatesAllowOnlyOneActiveTrip(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	usecase := create.New(db.repo, db.tx, db.logger, db.observer)
	results := simultaneousCreates(t.Context(), usecase, createInput())
	first, second := results[0], results[1]
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || first.result.Trip.ID == uuid.Nil || !errors.Is(second.err, trip.ErrDriverBusy) {
		t.Fatalf("concurrent outcomes = %+v / %+v, want one success and one busy driver", first, second)
	}
	if db.observer.created.Load() != 1 {
		t.Fatalf("created counter = %d, want 1", db.observer.created.Load())
	}
	assertRowCounts(t, db.pool, 1, 1, 0)
}

func TestConcurrentIdempotentCreatesReplayOneTrip(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	usecase := create.New(db.repo, db.tx, db.logger, db.observer)
	input := createInput()
	key := uuid.New()
	input.IdempotencyKey = &key
	results := simultaneousCreates(t.Context(), usecase, input)
	if results[0].err != nil || results[1].err != nil {
		t.Fatalf("concurrent idempotent outcomes = %+v", results)
	}
	if results[0].result.Trip.ID != results[1].result.Trip.ID || results[0].result.Replayed == results[1].result.Replayed {
		t.Fatalf("expected one creation and one replay of the same trip, got %+v", results)
	}
	input.Price++
	_, err := usecase.Create(t.Context(), input)
	if !errors.Is(err, trip.ErrIdempotencyConflict) {
		t.Fatalf("reuse key with different payload = %v, want ErrIdempotencyConflict", err)
	}
	if db.observer.created.Load() != 1 {
		t.Fatalf("created counter = %d, want 1", db.observer.created.Load())
	}
	assertRowCounts(t, db.pool, 1, 1, 1)
}

func TestCreateRollsBackIdempotencyReservationOnBusyDriver(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	usecase := create.New(db.repo, db.tx, db.logger, db.observer)
	input := createInput()
	if _, err := usecase.Create(t.Context(), input); err != nil {
		t.Fatalf("create first trip: %v", err)
	}
	key := uuid.New()
	input.IdempotencyKey = &key
	if _, err := usecase.Create(t.Context(), input); !errors.Is(err, trip.ErrDriverBusy) {
		t.Fatalf("create with occupied driver = %v, want ErrDriverBusy", err)
	}
	assertRowCounts(t, db.pool, 1, 1, 0)
	input.DriverID = uuid.New()
	result, err := usecase.Create(t.Context(), input)
	if err != nil || result.Replayed {
		t.Fatalf("retry rolled-back key = %+v, %v, want a new trip", result, err)
	}
	assertRowCounts(t, db.pool, 2, 2, 1)
}

func TestConcurrentFinishesCommitOneTransition(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	value := activeTrip()
	if err := db.tx.Do(t.Context(), func(ctx context.Context) error {
		return insertTripAndHistory(ctx, db, value)
	}); err != nil {
		t.Fatalf("create active trip: %v", err)
	}
	usecase := finish.New(db.repo, db.tx, db.logger, db.observer)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := usecase.Finish(t.Context(), value.ID)
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first != nil {
		first, second = second, first
	}
	if first != nil || !errors.Is(second, trip.ErrTripCompleted) {
		t.Fatalf("finish outcomes = %v / %v, want success and ErrTripCompleted", first, second)
	}
	if db.observer.completed.Load() != 1 {
		t.Fatalf("completed counter = %d, want 1", db.observer.completed.Load())
	}
	assertRowCounts(t, db.pool, 1, 2, 0)
}
