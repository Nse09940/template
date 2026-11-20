//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=createposition_test
package createposition

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type tripRepository interface {
	UpdateLastPositionAt(ctx context.Context, id uuid.UUID, at time.Time) error
	InsertPosition(ctx context.Context, position model.Position) (model.Position, error)
}

type txManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type pendingRequests interface {
	Cancel(tripID uuid.UUID)
}

type observer interface {
	PositionSaved(context.Context)
}
