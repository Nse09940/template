package get_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/model"
	"github.com/Nse09940/template/internal/usecase/trip/get"
)

func TestGet(t *testing.T) {
	t.Parallel()
	for _, wantErr := range []error{nil, trip.ErrTripNotFound, errors.New("repository failed")} {
		t.Run(describe(wantErr), func(t *testing.T) {
			t.Parallel()
			repo := NewMocktripRepository(gomock.NewController(t))
			id := uuid.New()
			want := model.Trip{ID: id}
			repo.EXPECT().GetByID(gomock.Any(), id).Return(want, wantErr)
			got, err := get.New(repo).Get(context.Background(), id)
			if !errors.Is(err, wantErr) {
				t.Fatalf("error=%v want=%v", err, wantErr)
			}
			if err == nil && got.ID != id {
				t.Fatalf("trip=%+v", got)
			}
		})
	}
}

func describe(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}
