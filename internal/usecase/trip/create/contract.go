//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=create_test
package create

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
)

type tripRepository interface {
	Insert(ctx context.Context, trip model.Trip) error
	InsertStatusHistory(
		ctx context.Context,
		tripID uuid.UUID,
		fromStatus *trip.Status,
		toStatus trip.Status,
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
	GetCreateIdempotency(ctx context.Context, key uuid.UUID) (model.CreateIdempotency, error)
	GetByID(ctx context.Context, id uuid.UUID) (model.Trip, error)
}
type txManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}
type observer interface{ Created(context.Context) }
