//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=createposition_test
package createposition

import (
	"context"

	"github.com/Nse09940/template/internal/model"
)

type usecase interface {
	Save(ctx context.Context, position model.Position) (model.Position, error)
}
