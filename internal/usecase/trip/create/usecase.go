package create

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
)

const idempotencyKeyTTL = 24 * time.Hour

type Usecase struct {
	repository tripRepository
	txManager  txManager
	logger     *slog.Logger
	observer   observer
	now        func() time.Time
}

func New(repository tripRepository, tx txManager, logger *slog.Logger, observer observer) *Usecase {
	return &Usecase{repository: repository, txManager: tx, logger: logger, observer: observer, now: time.Now}
}

func (s *Usecase) Create(ctx context.Context, input model.CreateInput) (model.CreateResult, error) {
	now := s.now().UTC().Truncate(time.Microsecond)
	created := model.Trip{
		ID:         uuid.New(),
		UserID:     input.UserID,
		DriverID:   input.DriverID,
		StartPoint: input.StartPoint,
		EndPoint:   input.EndPoint,
		Price:      input.Price,
		Status:     trip.StatusActive,
		StartedAt:  now,
	}
	if err := created.Validate(); err != nil {
		return model.CreateResult{}, err
	}
	var result model.CreateResult

	var requestHash []byte
	if input.IdempotencyKey != nil {
		encoded, err := json.Marshal(struct {
			UserID     uuid.UUID         `json:"user_id"`
			DriverID   uuid.UUID         `json:"driver_id"`
			StartPoint model.Coordinates `json:"start_point"`
			EndPoint   model.Coordinates `json:"end_point"`
			Price      int64             `json:"price"`
		}{
			UserID:     input.UserID,
			DriverID:   input.DriverID,
			StartPoint: input.StartPoint,
			EndPoint:   input.EndPoint,
			Price:      input.Price,
		})
		if err != nil {
			return model.CreateResult{}, fmt.Errorf("encode create trip idempotency payload: %w", err)
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
					return trip.ErrIdempotencyConflict
				}

				existing, err := s.repository.GetByID(txCtx, stored.TripID)
				if err != nil {
					return fmt.Errorf("get idempotent trip: %w", err)
				}
				result = model.CreateResult{Trip: existing, Replayed: true}
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
			trip.StatusActive,
			"trip created",
			now,
		); err != nil {
			return fmt.Errorf("insert initial status history: %w", err)
		}
		result = model.CreateResult{Trip: created}
		return nil
	})
	if err != nil {
		return model.CreateResult{}, fmt.Errorf("create trip transaction: %w", err)
	}

	if !result.Replayed {
		s.observer.Created(ctx)
		s.logger.InfoContext(ctx, "trip created", "trip_id", result.Trip.ID)
	}
	return result, nil
}
