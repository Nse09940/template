package get

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type Usecase struct{ repository tripRepository }

func New(repository tripRepository) *Usecase { return &Usecase{repository: repository} }
func (s *Usecase) Get(ctx context.Context, id uuid.UUID) (model.Trip, error) {
	found, err := s.repository.GetByID(ctx, id)
	if err != nil {
		return model.Trip{}, fmt.Errorf("get trip: %w", err)
	}

	return found, nil
}
