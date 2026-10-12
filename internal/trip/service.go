package trip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"

	idempotencyKeyTTL = 24 * time.Hour
)

var (
	ErrNotFound            = errors.New("trip not found")
	ErrCompleted           = errors.New("trip already completed")
	ErrDriverBusy          = errors.New("driver already has an active trip")
	ErrIdempotencyConflict = errors.New("idempotency key was used with a different request")
)

type Coordinates struct {
	Latitude  float64
	Longitude float64
}

type Trip struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	DriverID       uuid.UUID
	StartPoint     Coordinates
	EndPoint       Coordinates
	Price          int64
	Status         Status
	StartedAt      time.Time
	FinishedAt     *time.Time
	LastPositionAt *time.Time
}

type CreateInput struct {
	UserID         uuid.UUID
	DriverID       uuid.UUID
	StartPoint     Coordinates
	EndPoint       Coordinates
	Price          int64
	IdempotencyKey *uuid.UUID
}

type CreateResult struct {
	Trip     Trip
	Replayed bool
}

type CreateIdempotency struct {
	RequestHash []byte
	TripID      uuid.UUID
}

type Repository interface {
	Insert(ctx context.Context, trip Trip) error
	InsertStatusHistory(
		ctx context.Context,
		tripID uuid.UUID,
		fromStatus *Status,
		toStatus Status,
		reason string,
		changedAt time.Time,
	) error
	DeleteExpiredCreateIdempotency(ctx context.Context, now time.Time) error
	TryReserveCreateIdempotency(
		ctx context.Context,
		key uuid.UUID,
		requestHash []byte,
		tripID uuid.UUID,
		createdAt time.Time,
		expiresAt time.Time,
	) (bool, error)
	GetCreateIdempotency(ctx context.Context, key uuid.UUID) (CreateIdempotency, error)
	GetByID(ctx context.Context, id uuid.UUID) (Trip, error)
	FinishActive(ctx context.Context, id uuid.UUID, finishedAt time.Time) (Trip, error)
}

type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	repository Repository
	txManager  TxManager
	now        func() time.Time
}

func NewService(repository Repository, txManager TxManager) *Service {
	return &Service{
		repository: repository,
		txManager:  txManager,
		now:        time.Now,
	}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	now := s.now().UTC().Truncate(time.Microsecond)
	created := Trip{
		ID:         uuid.New(),
		UserID:     input.UserID,
		DriverID:   input.DriverID,
		StartPoint: input.StartPoint,
		EndPoint:   input.EndPoint,
		Price:      input.Price,
		Status:     StatusActive,
		StartedAt:  now,
	}
	var result CreateResult

	var requestHash []byte
	if input.IdempotencyKey != nil {
		encoded, err := json.Marshal(struct {
			UserID     uuid.UUID   `json:"user_id"`
			DriverID   uuid.UUID   `json:"driver_id"`
			StartPoint Coordinates `json:"start_point"`
			EndPoint   Coordinates `json:"end_point"`
			Price      int64       `json:"price"`
		}{
			UserID:     input.UserID,
			DriverID:   input.DriverID,
			StartPoint: input.StartPoint,
			EndPoint:   input.EndPoint,
			Price:      input.Price,
		})
		if err != nil {
			return CreateResult{}, fmt.Errorf("encode create trip idempotency payload: %w", err)
		}
		sum := sha256.Sum256(encoded)
		requestHash = sum[:]
	}

	err := s.txManager.Do(ctx, func(txCtx context.Context) error {
		if input.IdempotencyKey != nil {
			if err := s.repository.DeleteExpiredCreateIdempotency(
				txCtx,
				now,
			); err != nil {
				return fmt.Errorf("delete expired create idempotency keys: %w", err)
			}

			reserved, err := s.repository.TryReserveCreateIdempotency(
				txCtx,
				*input.IdempotencyKey,
				requestHash,
				created.ID,
				now,
				now.Add(idempotencyKeyTTL),
			)
			if err != nil {
				return fmt.Errorf("reserve create idempotency key: %w", err)
			}
			if !reserved {
				stored, err := s.repository.GetCreateIdempotency(txCtx, *input.IdempotencyKey)
				if err != nil {
					return fmt.Errorf("get create idempotency key: %w", err)
				}
				if !bytes.Equal(stored.RequestHash, requestHash) {
					return ErrIdempotencyConflict
				}

				existing, err := s.repository.GetByID(txCtx, stored.TripID)
				if err != nil {
					return fmt.Errorf("get idempotent trip: %w", err)
				}
				result = CreateResult{Trip: existing, Replayed: true}
				return nil
			}
		}

		if err := s.repository.Insert(txCtx, created); err != nil {
			return fmt.Errorf("insert trip: %w", err)
		}
		if err := s.repository.InsertStatusHistory(
			txCtx,
			created.ID,
			nil,
			StatusActive,
			"trip created",
			now,
		); err != nil {
			return fmt.Errorf("insert initial status history: %w", err)
		}
		result = CreateResult{Trip: created}
		return nil
	})
	if err != nil {
		return CreateResult{}, fmt.Errorf("create trip transaction: %w", err)
	}

	return result, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Trip, error) {
	found, err := s.repository.GetByID(ctx, id)
	if err != nil {
		return Trip{}, fmt.Errorf("get trip: %w", err)
	}

	return found, nil
}

func (s *Service) Finish(ctx context.Context, id uuid.UUID) (Trip, error) {
	finishedAt := s.now().UTC().Truncate(time.Microsecond)
	var finished Trip

	err := s.txManager.Do(ctx, func(txCtx context.Context) error {
		updated, err := s.repository.FinishActive(txCtx, id, finishedAt)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return fmt.Errorf("finish active trip: %w", err)
			}

			existing, getErr := s.repository.GetByID(txCtx, id)
			if getErr != nil {
				if errors.Is(getErr, ErrNotFound) {
					return ErrNotFound
				}
				return fmt.Errorf("get trip after failed finish: %w", getErr)
			}
			if existing.Status == StatusCompleted {
				return ErrCompleted
			}
			return fmt.Errorf("trip %s has unexpected status %q", id, existing.Status)
		}

		from := StatusActive
		if err := s.repository.InsertStatusHistory(
			txCtx,
			id,
			&from,
			StatusCompleted,
			"trip finished",
			finishedAt,
		); err != nil {
			return fmt.Errorf("insert completed status history: %w", err)
		}

		finished = updated
		return nil
	})
	if err != nil {
		return Trip{}, fmt.Errorf("finish trip transaction: %w", err)
	}

	return finished, nil
}
