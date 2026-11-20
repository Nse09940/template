package positions

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	queued    = "queued"
	duplicate = "duplicate"
	dropped   = "dropped"
	success   = "success"
	failed    = "error"
	cancelled = "cancelled"
)

type poolMetrics struct {
	staleChecks metric.Int64Counter
	requests    metric.Int64Counter
	duration    metric.Float64Histogram
}

func newPoolMetrics(meter metric.Meter, p *Pool) (poolMetrics, error) {
	var signals poolMetrics
	var err error
	if signals.staleChecks, err = meter.Int64Counter("tripgo.stale_checks", metric.WithUnit("{trip}")); err != nil {
		return poolMetrics{}, fmt.Errorf("create stale checks counter: %w", err)
	}
	if signals.requests, err = meter.Int64Counter("tripgo.position_requests", metric.WithUnit("{request}")); err != nil {
		return poolMetrics{}, fmt.Errorf("create position requests counter: %w", err)
	}
	if signals.duration, err = meter.Float64Histogram("tripgo.position_requests.duration", metric.WithUnit("s")); err != nil {
		return poolMetrics{}, fmt.Errorf("create position requests duration histogram: %w", err)
	}
	_, err = meter.Int64ObservableGauge("tripgo.position_requests.queue.size", metric.WithUnit("{task}"), metric.WithInt64Callback(
		func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(int64(len(p.queue)))
			return nil
		},
	))
	if err != nil {
		return poolMetrics{}, fmt.Errorf("create position queue size gauge: %w", err)
	}
	_, err = meter.Int64ObservableGauge("tripgo.position_requests.workers.active", metric.WithUnit("{worker}"), metric.WithInt64Callback(
		func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(p.active.Load())
			return nil
		},
	))
	if err != nil {
		return poolMetrics{}, fmt.Errorf("create position workers active gauge: %w", err)
	}
	return signals, nil
}

func resultAttribute(result string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("result", result))
}
