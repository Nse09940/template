package listpositions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type Usecase struct{ repository tripRepository }

func New(repository tripRepository) *Usecase { return &Usecase{repository: repository} }

func (u *Usecase) List(ctx context.Context, tripID uuid.UUID) ([]model.Position, error) {
	if _, err := u.repository.GetByID(ctx, tripID); err != nil {
		return nil, fmt.Errorf("get trip for route: %w", err)
	}
	positions, err := u.repository.ListPositions(ctx, tripID)
	if err != nil {
		return nil, fmt.Errorf("list trip positions: %w", err)
	}
	return positions, nil
}
