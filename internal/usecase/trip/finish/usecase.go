package finish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
)

type Usecase struct {
	repository tripRepository
	txManager  txManager
	logger     *slog.Logger
	observer   observer
}

func New(repository tripRepository, tx txManager, logger *slog.Logger, observer observer) *Usecase {
	return &Usecase{repository: repository, txManager: tx, logger: logger, observer: observer}
}

func (s *Usecase) Finish(ctx context.Context, id uuid.UUID) (model.Trip, error) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	var finished model.Trip
	err := s.txManager.Do(ctx, func(txCtx context.Context) error {
		existing, err := s.repository.GetByID(txCtx, id)
		if err != nil {
			return fmt.Errorf("get trip for finish: %w", err)
		}
		if err := existing.Finish(at); err != nil {
			return err
		}
		updated, err := s.repository.FinishActive(txCtx, id, *existing.FinishedAt)
		if errors.Is(err, trip.ErrTripNotFound) {
			return trip.ErrTripCompleted
		}
		if err != nil {
			return fmt.Errorf("finish active trip: %w", err)
		}
		from := trip.StatusActive
		if err := s.repository.InsertStatusHistory(txCtx, id, &from, trip.StatusCompleted, "trip finished", at); err != nil {
			return fmt.Errorf("insert completed status history: %w", err)
		}
		finished = updated
		return nil
	})
	if err != nil {
		return model.Trip{}, fmt.Errorf("finish trip transaction: %w", err)
	}
	s.observer.Completed(ctx)
	s.logger.InfoContext(ctx, "trip finished", "trip_id", id)
	return finished, nil
}
