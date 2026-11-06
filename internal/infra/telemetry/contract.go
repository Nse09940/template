package telemetry

import "context"

type activeTripsRepository interface {
	CountActive(context.Context) (int64, error)
}
