package trip

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type transactionMarker struct{}

type fakeTxManager struct {
	calls int
}

func (m *fakeTxManager) Do(ctx context.Context, fn func(context.Context) error) error {
	m.calls++
	return fn(context.WithValue(ctx, transactionMarker{}, true))
}

type fakeRepository struct {
	insert                         func(context.Context, Trip) error
	insertStatusHistory            func(context.Context, uuid.UUID, *Status, Status, string, time.Time) error
	deleteExpiredCreateIdempotency func(context.Context, uuid.UUID, time.Time) error
	tryReserveCreateIdempotency    func(
		context.Context,
		uuid.UUID,
		[]byte,
		uuid.UUID,
		time.Time,
		time.Time,
	) (bool, error)
	getCreateIdempotency func(context.Context, uuid.UUID) (CreateIdempotency, error)
	getByID              func(context.Context, uuid.UUID) (Trip, error)
	finishActive         func(context.Context, uuid.UUID, time.Time) (Trip, error)
}

func (r fakeRepository) DeleteExpiredCreateIdempotency(
	ctx context.Context,
	key uuid.UUID,
	now time.Time,
) error {
	return r.deleteExpiredCreateIdempotency(ctx, key, now)
}

func (r fakeRepository) TryReserveCreateIdempotency(
	ctx context.Context,
	key uuid.UUID,
	requestHash []byte,
	tripID uuid.UUID,
	createdAt time.Time,
	expiresAt time.Time,
) (bool, error) {
	return r.tryReserveCreateIdempotency(
		ctx,
		key,
		requestHash,
		tripID,
		createdAt,
		expiresAt,
	)
}

func (r fakeRepository) GetCreateIdempotency(
	ctx context.Context,
	key uuid.UUID,
) (CreateIdempotency, error) {
	return r.getCreateIdempotency(ctx, key)
}

func (r fakeRepository) Insert(ctx context.Context, value Trip) error {
	return r.insert(ctx, value)
}

func (r fakeRepository) InsertStatusHistory(
	ctx context.Context,
	id uuid.UUID,
	from *Status,
	to Status,
	reason string,
	changedAt time.Time,
) error {
	return r.insertStatusHistory(ctx, id, from, to, reason, changedAt)
}

func (r fakeRepository) GetByID(ctx context.Context, id uuid.UUID) (Trip, error) {
	return r.getByID(ctx, id)
}

func (r fakeRepository) FinishActive(
	ctx context.Context,
	id uuid.UUID,
	finishedAt time.Time,
) (Trip, error) {
	return r.finishActive(ctx, id, finishedAt)
}

func TestCreateWritesTripAndHistoryInOneTransaction(t *testing.T) {
	t.Parallel()

	txManager := &fakeTxManager{}
	var inserted Trip
	repository := fakeRepository{
		insert: func(ctx context.Context, value Trip) error {
			if marker, _ := ctx.Value(transactionMarker{}).(bool); !marker {
				t.Fatal("Insert called outside transaction context")
			}
			inserted = value
			return nil
		},
		insertStatusHistory: func(
			ctx context.Context,
			id uuid.UUID,
			from *Status,
			to Status,
			_ string,
			changedAt time.Time,
		) error {
			if marker, _ := ctx.Value(transactionMarker{}).(bool); !marker {
				t.Fatal("InsertStatusHistory called outside transaction context")
			}
			if id != inserted.ID || from != nil || to != StatusActive || !changedAt.Equal(inserted.StartedAt) {
				t.Fatalf("unexpected history: id=%s from=%v to=%s changed=%s", id, from, to, changedAt)
			}
			return nil
		},
	}
	service := NewService(repository, txManager)

	result, err := service.Create(context.Background(), CreateInput{
		UserID:   uuid.New(),
		DriverID: uuid.New(),
		StartPoint: Coordinates{
			Latitude:  59.9,
			Longitude: 30.3,
		},
		EndPoint: Coordinates{
			Latitude:  59.8,
			Longitude: 30.4,
		},
		Price: 1450,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if txManager.calls != 1 {
		t.Fatalf("transaction calls = %d", txManager.calls)
	}
	if result.Trip.ID == uuid.Nil || result.Trip.Status != StatusActive || result.Trip.FinishedAt != nil {
		t.Fatalf("unexpected created trip: %+v", result.Trip)
	}
}

func TestCreateReservesIdempotencyKeyForOneDay(t *testing.T) {
	t.Parallel()

	key := uuid.New()
	deleted := false
	reserved := false
	repository := fakeRepository{
		deleteExpiredCreateIdempotency: func(_ context.Context, gotKey uuid.UUID, _ time.Time) error {
			deleted = true
			if gotKey != key {
				t.Fatalf("delete key = %s, want %s", gotKey, key)
			}
			return nil
		},
		tryReserveCreateIdempotency: func(
			_ context.Context,
			gotKey uuid.UUID,
			requestHash []byte,
			tripID uuid.UUID,
			createdAt time.Time,
			expiresAt time.Time,
		) (bool, error) {
			reserved = true
			if gotKey != key || len(requestHash) != sha256.Size || tripID == uuid.Nil {
				t.Fatalf("unexpected reservation: key=%s hash=%x trip=%s", gotKey, requestHash, tripID)
			}
			if expiresAt.Sub(createdAt) != idempotencyKeyTTL {
				t.Fatalf("TTL = %s, want %s", expiresAt.Sub(createdAt), idempotencyKeyTTL)
			}
			return true, nil
		},
		insert: func(context.Context, Trip) error { return nil },
		insertStatusHistory: func(
			context.Context,
			uuid.UUID,
			*Status,
			Status,
			string,
			time.Time,
		) error {
			return nil
		},
	}
	service := NewService(repository, &fakeTxManager{})

	result, err := service.Create(context.Background(), CreateInput{
		UserID:         uuid.New(),
		DriverID:       uuid.New(),
		StartPoint:     Coordinates{Latitude: 59.9, Longitude: 30.3},
		EndPoint:       Coordinates{Latitude: 59.8, Longitude: 30.4},
		Price:          1450,
		IdempotencyKey: &key,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !deleted || !reserved || result.Replayed {
		t.Fatalf("deleted=%t reserved=%t replayed=%t", deleted, reserved, result.Replayed)
	}
}

func TestCreateReplaysSameIdempotencyRequest(t *testing.T) {
	t.Parallel()

	key := uuid.New()
	existing := Trip{ID: uuid.New(), Status: StatusActive}
	var requestHash []byte
	repository := fakeRepository{
		deleteExpiredCreateIdempotency: func(context.Context, uuid.UUID, time.Time) error {
			return nil
		},
		tryReserveCreateIdempotency: func(
			_ context.Context,
			_ uuid.UUID,
			hash []byte,
			_ uuid.UUID,
			_ time.Time,
			_ time.Time,
		) (bool, error) {
			requestHash = append([]byte(nil), hash...)
			return false, nil
		},
		getCreateIdempotency: func(context.Context, uuid.UUID) (CreateIdempotency, error) {
			return CreateIdempotency{RequestHash: requestHash, TripID: existing.ID}, nil
		},
		getByID: func(_ context.Context, id uuid.UUID) (Trip, error) {
			if id != existing.ID {
				t.Fatalf("trip ID = %s, want %s", id, existing.ID)
			}
			return existing, nil
		},
	}
	service := NewService(repository, &fakeTxManager{})

	result, err := service.Create(context.Background(), CreateInput{
		UserID:         uuid.New(),
		DriverID:       uuid.New(),
		StartPoint:     Coordinates{Latitude: 59.9, Longitude: 30.3},
		EndPoint:       Coordinates{Latitude: 59.8, Longitude: 30.4},
		Price:          1450,
		IdempotencyKey: &key,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !result.Replayed || result.Trip.ID != existing.ID {
		t.Fatalf("unexpected replay result: %+v", result)
	}
}

func TestCreateRejectsReusedIdempotencyKeyWithDifferentRequest(t *testing.T) {
	t.Parallel()

	key := uuid.New()
	repository := fakeRepository{
		deleteExpiredCreateIdempotency: func(context.Context, uuid.UUID, time.Time) error {
			return nil
		},
		tryReserveCreateIdempotency: func(
			context.Context,
			uuid.UUID,
			[]byte,
			uuid.UUID,
			time.Time,
			time.Time,
		) (bool, error) {
			return false, nil
		},
		getCreateIdempotency: func(context.Context, uuid.UUID) (CreateIdempotency, error) {
			return CreateIdempotency{RequestHash: make([]byte, sha256.Size), TripID: uuid.New()}, nil
		},
	}
	service := NewService(repository, &fakeTxManager{})

	_, err := service.Create(context.Background(), CreateInput{
		UserID:         uuid.New(),
		DriverID:       uuid.New(),
		StartPoint:     Coordinates{Latitude: 59.9, Longitude: 30.3},
		EndPoint:       Coordinates{Latitude: 59.8, Longitude: 30.4},
		Price:          1450,
		IdempotencyKey: &key,
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("Create error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestFinishDistinguishesCompletedFromMissing(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	repository := fakeRepository{
		finishActive: func(context.Context, uuid.UUID, time.Time) (Trip, error) {
			return Trip{}, ErrNotFound
		},
		getByID: func(context.Context, uuid.UUID) (Trip, error) {
			return Trip{ID: id, Status: StatusCompleted}, nil
		},
	}
	service := NewService(repository, &fakeTxManager{})

	_, err := service.Finish(context.Background(), id)
	if !errors.Is(err, ErrCompleted) {
		t.Fatalf("Finish error = %v, want ErrCompleted", err)
	}
}
