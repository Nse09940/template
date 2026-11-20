package createposition

import (
	"context"
	"fmt"
	"time"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
)

type Usecase struct {
	repository tripRepository
	txManager  txManager
	pending    pendingRequests
	observer   observer
	now        func() time.Time
}

func New(repository tripRepository, tx txManager, pending pendingRequests, observer observer, now func() time.Time) *Usecase {
	return &Usecase{repository: repository, txManager: tx, pending: pending, observer: observer, now: now}
}

func (u *Usecase) Save(ctx context.Context, position model.Position) (model.Position, error) {
	if err := position.Coordinates.Validate("coordinates"); err != nil {
		return model.Position{}, err
	}
	if position.RecordedAt.After(u.now().Add(time.Minute)) {
		return model.Position{}, &trip.ValidationError{Field: "recorded_at", Message: "must be at most one minute in the future"}
	}
	position.RecordedAt = position.RecordedAt.UTC().Truncate(time.Microsecond)

	var saved model.Position
	if err := u.txManager.Do(ctx, func(txCtx context.Context) error {
		// Updating the active trip first serializes acceptance with trip completion.
		if err := u.repository.UpdateLastPositionAt(txCtx, position.TripID, position.RecordedAt); err != nil {
			return fmt.Errorf("update trip last position: %w", err)
		}
		result, err := u.repository.InsertPosition(txCtx, position)
		if err != nil {
			return fmt.Errorf("insert trip position: %w", err)
		}
		saved = result
		return nil
	}); err != nil {
		return model.Position{}, fmt.Errorf("save trip position transaction: %w", err)
	}

	u.pending.Cancel(position.TripID)
	u.observer.PositionSaved(ctx)
	return saved, nil
}
