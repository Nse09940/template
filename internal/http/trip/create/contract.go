//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=create_test
package create

import (
	"context"

	"github.com/Nse09940/template/internal/model"
)

type usecase interface {
	Create(ctx context.Context, input model.CreateInput) (model.CreateResult, error)
}
