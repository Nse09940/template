package positions

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/model"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestDeduplicatesOnLaterTicksAndDropsWithoutBlocking(t *testing.T) {
	h := newPoolHarness(t, 1, 1)
	busy, waiting, overflow := staleTrip(), staleTrip(), staleTrip()
	started := make(chan uuid.UUID, 1)
	release := make(chan struct{})
	h.blockRequest(busy, started, release, nil)
	h.push.EXPECT().RequestPosition(gomock.Any(), waiting.TripID, waiting.DriverID).Return(nil)
	h.pool.Start(h.ctx)
	h.scan(busy)
	await(t, h.ctx, started)
	h.scan(waiting)
	h.scan(waiting, overflow)
	h.scanBarrier()

	metrics := h.collectMetrics()
	assertCounter(t, metrics, "tripgo.stale_checks", queued, 2)
	assertCounter(t, metrics, "tripgo.stale_checks", duplicate, 1)
	assertCounter(t, metrics, "tripgo.stale_checks", dropped, 1)
	assertGauge(t, metrics, "tripgo.position_requests.queue.size", 1)
	close(release)
	h.shutdown()
}

func TestCancelsQueuedRequestsAndKeepsNewestQueueEntry(t *testing.T) {
	h := newPoolHarness(t, 1, 3)
	busy, cancelledTrip, requeued := staleTrip(), staleTrip(), staleTrip()
	started := make(chan uuid.UUID, 1)
	release := make(chan struct{})
	h.blockRequest(busy, started, release, nil)
	replacement := requeued
	replacement.DriverID = uuid.New()
	h.push.EXPECT().RequestPosition(gomock.Any(), replacement.TripID, replacement.DriverID).Return(nil)
	h.pool.Start(h.ctx)
	h.scan(busy)
	await(t, h.ctx, started)
	h.scan(cancelledTrip, requeued)
	h.scanBarrier()
	h.pool.Cancel(cancelledTrip.TripID)
	h.pool.Cancel(requeued.TripID)
	h.scan(replacement)
	h.scanBarrier()

	close(release)
	h.shutdown()
	metrics := h.collectMetrics()
	assertCounter(t, metrics, "tripgo.position_requests", cancelled, 2)
	assertCounter(t, metrics, "tripgo.position_requests", success, 2)
}

func TestSaveDuringStaleQuerySuppressesItsResult(t *testing.T) {
	h := newPoolHarness(t, 1, 1)
	trip := staleTrip()
	queryStarted := make(chan struct{})
	queryRelease := make(chan struct{})
	h.repo.EXPECT().FindStaleTrips(gomock.Any(), h.pool.cfg.StaleAfter).DoAndReturn(
		func(ctx context.Context, _ time.Duration) ([]model.StaleTrip, error) {
			close(queryStarted)
			select {
			case <-queryRelease:
				return []model.StaleTrip{trip}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	h.pool.Start(h.ctx)
	h.tick()
	await(t, h.ctx, queryStarted)
	h.pool.Cancel(trip.TripID)
	close(queryRelease)
	h.scanBarrier()
	h.shutdown()
}

func TestFixedWorkerCountAndRecoveryAfterPushError(t *testing.T) {
	const workers, total = 3, 7
	h := newPoolHarness(t, workers, total)
	started := make(chan uuid.UUID, total)
	release := make(chan struct{})
	trips := make([]model.StaleTrip, total)
	for i := range trips {
		trips[i] = staleTrip()
		var callErr error
		if i == 0 {
			callErr = errors.New("push service unavailable")
		}
		h.blockRequest(trips[i], started, release, callErr)
	}
	h.pool.Start(h.ctx)
	h.scan(trips...)
	for range workers {
		await(t, h.ctx, started)
	}
	h.scanBarrier()

	metrics := h.collectMetrics()
	assertGauge(t, metrics, "tripgo.position_requests.workers.active", workers)
	assertGauge(t, metrics, "tripgo.position_requests.queue.size", total-workers)
	close(release)
	h.shutdown()
	metrics = h.collectMetrics()
	assertGauge(t, metrics, "tripgo.position_requests.workers.active", 0)
	assertGauge(t, metrics, "tripgo.position_requests.queue.size", 0)
	assertCounter(t, metrics, "tripgo.position_requests", success, total-1)
	assertCounter(t, metrics, "tripgo.position_requests", failed, 1)
	assertHistogramCount(t, metrics, "tripgo.position_requests.duration", total)
	assertWorkerSpanLinks(t, h.spans.Ended(), total)
}

func TestParentCancellationStopsScannerAndLetsWorkersDrain(t *testing.T) {
	h := newPoolHarness(t, 1, 1)
	busy, waiting := staleTrip(), staleTrip()
	started := make(chan uuid.UUID, 1)
	release := make(chan struct{})
	h.blockRequest(busy, started, release, nil)
	h.push.EXPECT().RequestPosition(gomock.Any(), waiting.TripID, waiting.DriverID).Return(nil)
	parent, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.pool.Start(parent)
	h.scan(busy)
	await(t, h.ctx, started)
	h.scan(waiting)
	queryStarted := make(chan struct{})
	h.repo.EXPECT().FindStaleTrips(gomock.Any(), h.pool.cfg.StaleAfter).DoAndReturn(
		func(ctx context.Context, _ time.Duration) ([]model.StaleTrip, error) {
			close(queryStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	h.tick()
	await(t, h.ctx, queryStarted)

	cancel()
	await(t, h.ctx, h.tickerStopped)
	close(release)
	h.shutdown()
	assertCounter(t, h.collectMetrics(), "tripgo.position_requests", success, 2)
}

func TestShutdownDeadlineCancelsActiveRequest(t *testing.T) {
	h := newPoolHarness(t, 1, 1)
	busy, waiting := staleTrip(), staleTrip()
	started := make(chan uuid.UUID, 1)
	h.blockRequest(busy, started, make(chan struct{}), nil)
	h.pool.Start(h.ctx)
	h.scan(busy)
	await(t, h.ctx, started)
	h.scan(waiting)
	h.scanBarrier()

	expired, cancel := context.WithDeadline(h.ctx, time.Time{})
	defer cancel()
	if err := h.pool.Shutdown(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	await(t, h.ctx, h.tickerStopped)
	metrics := h.collectMetrics()
	assertGauge(t, metrics, "tripgo.position_requests.queue.size", 0)
	assertCounter(t, metrics, "tripgo.position_requests", failed, 1)
}

type poolHarness struct {
	t             *testing.T
	ctx           context.Context
	pool          *Pool
	repo          *MockstaleTripsRepository
	push          *MockpushGateway
	ticks         chan time.Time
	tickerStopped chan struct{}
	reader        *metric.ManualReader
	spans         *tracetest.SpanRecorder
}

func newPoolHarness(t *testing.T, workers, queueSize int) *poolHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	mocks := gomock.NewController(t)
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	spans := tracetest.NewSpanRecorder()
	tracerProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spans))
	t.Cleanup(func() {
		cleanup := context.WithoutCancel(ctx)
		if err := errors.Join(meterProvider.Shutdown(cleanup), tracerProvider.Shutdown(cleanup)); err != nil {
			t.Errorf("shutdown telemetry: %v", err)
		}
	})
	repo, push := NewMockstaleTripsRepository(mocks), NewMockpushGateway(mocks)
	cfg := Config{CheckInterval: 30 * time.Second, StaleAfter: time.Minute, RequestTimeout: time.Hour, Workers: workers, QueueSize: queueSize}
	pool, err := New(cfg, repo, push, slog.New(slog.DiscardHandler), tracerProvider.Tracer("test"), meterProvider.Meter("test"))
	if err != nil {
		t.Fatalf("initialize pool: %v", err)
	}
	ticks := make(chan time.Time)
	tickerStopped := make(chan struct{})
	clock := NewMockticker(mocks)
	clock.EXPECT().Ticks().Return((<-chan time.Time)(ticks))
	clock.EXPECT().Stop().Do(func() { close(tickerStopped) })
	pool.newTicker = func(time.Duration) ticker { return clock }
	h := &poolHarness{t: t, ctx: ctx, pool: pool, repo: repo, push: push, ticks: ticks, tickerStopped: tickerStopped, reader: reader, spans: spans}
	t.Cleanup(h.shutdown)
	return h
}

func staleTrip() model.StaleTrip {
	return model.StaleTrip{TripID: uuid.New(), DriverID: uuid.New()}
}

func (h *poolHarness) blockRequest(trip model.StaleTrip, started chan<- uuid.UUID, release <-chan struct{}, callErr error) {
	h.push.EXPECT().RequestPosition(gomock.Any(), trip.TripID, trip.DriverID).DoAndReturn(
		func(ctx context.Context, tripID, _ uuid.UUID) error {
			started <- tripID
			select {
			case <-release:
				return callErr
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	)
}

func (h *poolHarness) scan(trips ...model.StaleTrip) {
	h.t.Helper()
	h.repo.EXPECT().FindStaleTrips(gomock.Any(), h.pool.cfg.StaleAfter).Return(trips, nil)
	h.tick()
}

func (h *poolHarness) scanBarrier() {
	h.t.Helper()
	entered := make(chan struct{})
	h.repo.EXPECT().FindStaleTrips(gomock.Any(), h.pool.cfg.StaleAfter).DoAndReturn(
		func(context.Context, time.Duration) ([]model.StaleTrip, error) {
			close(entered)
			return nil, nil
		},
	)
	h.tick()
	await(h.t, h.ctx, entered)
}

func (h *poolHarness) tick() {
	h.t.Helper()
	select {
	case h.ticks <- time.Time{}:
	case <-h.ctx.Done():
		h.t.Fatalf("scanner did not accept tick: %v", h.ctx.Err())
	}
}

func (h *poolHarness) shutdown() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(h.ctx), time.Second)
	defer cancel()
	if err := h.pool.Shutdown(ctx); err != nil {
		h.t.Errorf("shutdown pool: %v", err)
	}
}

func await[T any](t *testing.T, ctx context.Context, signal <-chan T) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("expected goroutine signal: %v", ctx.Err())
	}
}

func (h *poolHarness) collectMetrics() map[string]metricdata.Aggregation {
	h.t.Helper()
	var collected metricdata.ResourceMetrics
	if err := h.reader.Collect(h.ctx, &collected); err != nil {
		h.t.Fatalf("read worker metrics: %v", err)
	}
	metrics := make(map[string]metricdata.Aggregation)
	for _, scope := range collected.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			metrics[instrument.Name] = instrument.Data
		}
	}
	return metrics
}

func assertCounter(t *testing.T, metrics map[string]metricdata.Aggregation, name, result string, want int64) {
	t.Helper()
	sum, ok := metrics[name].(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %s: expected integer counter", name)
	}
	for _, point := range sum.DataPoints {
		value, _ := point.Attributes.Value("result")
		if value.AsString() == result {
			if point.Value != want {
				t.Fatalf("%s[%s] = %d, want %d", name, result, point.Value, want)
			}
			return
		}
	}
	t.Fatalf("%s: missing counter value for %s", name, result)
}

func assertGauge(t *testing.T, metrics map[string]metricdata.Aggregation, name string, want int64) {
	t.Helper()
	gauge, ok := metrics[name].(metricdata.Gauge[int64])
	if !ok || len(gauge.DataPoints) != 1 {
		t.Fatalf("metric %s: expected gauge with one value", name)
	}
	if got := gauge.DataPoints[0].Value; got != want {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}

func assertHistogramCount(t *testing.T, metrics map[string]metricdata.Aggregation, name string, want uint64) {
	t.Helper()
	histogram, ok := metrics[name].(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %s: expected histogram", name)
	}
	var total uint64
	for _, point := range histogram.DataPoints {
		total += point.Count
	}
	if total != want {
		t.Fatalf("%s has %d measurements, want %d", name, total, want)
	}
}

func assertWorkerSpanLinks(t *testing.T, spans []trace.ReadOnlySpan, want int) {
	t.Helper()
	for _, span := range spans {
		if span.Name() != "positions.request" {
			continue
		}
		if span.Parent().IsValid() || len(span.Links()) != 1 {
			t.Fatalf("worker span has unexpected parent or links: %v", span.Links())
		}
		found := false
		for _, scan := range spans {
			if scan.Name() == "positions.scan" && scan.SpanContext().Equal(span.Links()[0].SpanContext) {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("worker span does not link to a recorded scan span")
		}
		want--
	}
	if want != 0 {
		t.Fatalf("difference between expected and observed worker spans = %d", want)
	}
}
