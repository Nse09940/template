//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=listpositions_test
package listpositions

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type tripRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (model.Trip, error)
	ListPositions(ctx context.Context, tripID uuid.UUID) ([]model.Position, error)
}
