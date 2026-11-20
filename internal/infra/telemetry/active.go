package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

func (t *Telemetry) ObserveActiveTrips(repository activeTripsRepository) error {
	meter := t.MeterProvider.Meter("trip-service")
	gauge, err := meter.Int64ObservableGauge("tripgo.trips.active", metric.WithUnit("{trip}"))
	if err != nil {
		return fmt.Errorf("create active-trips gauge: %w", err)
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		count, err := repository.CountActive(ctx)
		if err != nil {
			return err
		}
		observer.ObserveInt64(gauge, count)
		return nil
	}, gauge)
	if err != nil {
		return fmt.Errorf("register active-trips callback: %w", err)
	}
	return nil
}
