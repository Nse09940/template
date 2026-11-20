//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=positions

package positions

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/model"
)

type staleTripsRepository interface {
	FindStaleTrips(ctx context.Context, staleAfter time.Duration) ([]model.StaleTrip, error)
}

type pushGateway interface {
	RequestPosition(ctx context.Context, tripID, driverID uuid.UUID) error
}

type ticker interface {
	Ticks() <-chan time.Time
	Stop()
}

type clockTicker struct{ *time.Ticker }

func (t clockTicker) Ticks() <-chan time.Time { return t.C }
