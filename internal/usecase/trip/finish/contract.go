//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=finish_test
package finish

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
)

type tripRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (model.Trip, error)
	FinishActive(ctx context.Context, id uuid.UUID, at time.Time) (model.Trip, error)
	InsertStatusHistory(ctx context.Context, id uuid.UUID, from *trip.Status, to trip.Status, reason string, at time.Time) error
}
type txManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}
type observer interface{ Completed(context.Context) }
