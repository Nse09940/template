//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=finish_test
package finish

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type usecase interface {
	Finish(ctx context.Context, id uuid.UUID) (model.Trip, error)
}
