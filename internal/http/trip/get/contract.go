//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=get_test
package get

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type usecase interface {
	Get(ctx context.Context, id uuid.UUID) (model.Trip, error)
}
