package positions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Nse09940/template/internal/model"
)

type Config struct {
	CheckInterval  time.Duration
	StaleAfter     time.Duration
	RequestTimeout time.Duration
	Workers        int
	QueueSize      int
}

type task struct {
	trip    model.StaleTrip
	scan    trace.SpanContext
	started bool
}

// Pool finds stale trips and requests a position through a bounded set of workers.
type Pool struct {
	cfg       Config
	repo      staleTripsRepository
	push      pushGateway
	logger    *slog.Logger
	tracer    trace.Tracer
	metrics   poolMetrics
	newTicker func(time.Duration) ticker
	queue     chan *task
	active    atomic.Int64

	mu                  sync.Mutex
	pending             map[uuid.UUID]*task
	cancelledDuringScan map[uuid.UUID]struct{}

	start       sync.Once
	stopScan    context.CancelFunc
	stopWorkers context.CancelFunc
	done        chan struct{}
}

func New(cfg Config, repo staleTripsRepository, push pushGateway, logger *slog.Logger, tracer trace.Tracer, meter metric.Meter) (*Pool, error) {
	if cfg.CheckInterval <= 0 || cfg.StaleAfter <= 0 || cfg.RequestTimeout <= 0 || cfg.Workers <= 0 || cfg.QueueSize <= 0 {
		return nil, fmt.Errorf("position worker intervals, timeout, worker count and queue size must be positive")
	}
	p := &Pool{
		cfg:       cfg,
		repo:      repo,
		push:      push,
		logger:    logger,
		tracer:    tracer,
		newTicker: func(interval time.Duration) ticker { return clockTicker{time.NewTicker(interval)} },
		queue:     make(chan *task, cfg.QueueSize),
		pending:   make(map[uuid.UUID]*task),
		done:      make(chan struct{}),
	}
	metrics, err := newPoolMetrics(meter, p)
	if err != nil {
		return nil, err
	}
	p.metrics = metrics
	return p, nil
}

// Start runs the pool once. Cancelling ctx stops the scanner and lets the workers drain the queue.
func (p *Pool) Start(ctx context.Context) {
	p.start.Do(func() {
		scanCtx, stopScan := context.WithCancel(ctx)
		workerCtx, stopWorkers := context.WithCancel(context.WithoutCancel(ctx))
		p.stopScan, p.stopWorkers = stopScan, stopWorkers
		go p.run(scanCtx, workerCtx)
	})
}

// Shutdown stops scanning and waits for workers. At the deadline it cancels the remaining requests.
func (p *Pool) Shutdown(ctx context.Context) error {
	// If shutdown comes before startup, there are no goroutines to wait for.
	p.start.Do(func() { close(p.done) })
	if p.stopScan == nil {
		return nil
	}
	p.stopScan()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.stopWorkers()
		<-p.done
		return ctx.Err()
	}
}

// Cancel invalidates a queued request when a position has already been saved.
func (p *Pool) Cancel(tripID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancelledDuringScan != nil {
		// A query already in flight may still return this trip from an older snapshot.
		p.cancelledDuringScan[tripID] = struct{}{}
	}
	if pending := p.pending[tripID]; pending != nil && !pending.started {
		delete(p.pending, tripID)
	}
}

func (p *Pool) run(scanCtx, workerCtx context.Context) {
	defer close(p.done)
	defer p.stopWorkers()
	defer p.stopScan()

	var workers sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		workers.Go(func() { p.work(workerCtx) })
	}
	p.logger.InfoContext(workerCtx, "position workers started", "workers", p.cfg.Workers, "queue_size", p.cfg.QueueSize)
	p.scanLoop(scanCtx)
	close(p.queue)
	workers.Wait()
	// After a forced stop, discard buffered tasks that will be rediscovered on restart.
	for range p.queue {
	}
	p.mu.Lock()
	clear(p.pending)
	p.mu.Unlock()
	p.logger.InfoContext(workerCtx, "position workers stopped")
}

func (p *Pool) scanLoop(ctx context.Context) {
	ticker := p.newTicker(p.cfg.CheckInterval)
	defer ticker.Stop()
	ticks := ticker.Ticks()
	p.logger.InfoContext(ctx, "position scanner started", "interval", p.cfg.CheckInterval)
	defer p.logger.InfoContext(ctx, "position scanner stopped")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if ctx.Err() != nil {
				return
			}
			p.scan(ctx)
		}
	}
}

func (p *Pool) scan(ctx context.Context) {
	ctx, span := p.tracer.Start(ctx, "positions.scan", trace.WithNewRoot())
	defer span.End()

	p.mu.Lock()
	p.cancelledDuringScan = make(map[uuid.UUID]struct{})
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.cancelledDuringScan = nil
		p.mu.Unlock()
	}()

	trips, err := p.repo.FindStaleTrips(ctx, p.cfg.StaleAfter)
	if err != nil {
		if ctx.Err() == nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "could not select stale trips")
			p.logger.WarnContext(ctx, "could not select stale trips", "error", err)
		}
		return
	}

	for _, trip := range trips {
		if ctx.Err() != nil {
			return
		}
		result := p.enqueue(trip, span.SpanContext())
		if result != "" {
			p.metrics.staleChecks.Add(ctx, 1, resultAttribute(result))
		}
		if result == dropped {
			p.logger.WarnContext(ctx, "position request queue full", "trip_id", trip.TripID)
		}
	}
}

func (p *Pool) enqueue(trip model.StaleTrip, scan trace.SpanContext) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, cancelled := p.cancelledDuringScan[trip.TripID]; cancelled {
		return ""
	}
	if p.pending[trip.TripID] != nil {
		return duplicate
	}
	job := &task{trip: trip, scan: scan}
	select {
	case p.queue <- job:
		p.pending[trip.TripID] = job
		return queued
	default:
		return dropped
	}
}

func (p *Pool) work(ctx context.Context) {
	for task := range p.queue {
		if ctx.Err() != nil {
			return
		}
		p.process(ctx, task)
	}
}

func (p *Pool) process(ctx context.Context, job *task) {
	ctx, span := p.tracer.Start(ctx, "positions.request", trace.WithNewRoot(), trace.WithLinks(trace.Link{SpanContext: job.scan}))
	defer span.End()

	if !p.claim(job) {
		p.metrics.requests.Add(ctx, 1, resultAttribute(cancelled))
		return
	}
	defer p.complete(job)
	p.active.Add(1)
	defer p.active.Add(-1)

	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	started := time.Now()
	err := p.push.RequestPosition(requestCtx, job.trip.TripID, job.trip.DriverID)
	elapsed := time.Since(started).Seconds()
	result := success
	if err != nil {
		result = failed
		span.RecordError(err)
		span.SetStatus(codes.Error, "push service request failed")
		p.logger.WarnContext(ctx, "push service request failed", "trip_id", job.trip.TripID, "error", err)
	}
	p.metrics.requests.Add(ctx, 1, resultAttribute(result))
	p.metrics.duration.Record(ctx, elapsed, resultAttribute(result))
}

func (p *Pool) claim(job *task) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	// An older, cancelled task can still occupy a channel slot after the trip is requeued.
	if p.pending[job.trip.TripID] != job {
		return false
	}
	job.started = true
	return true
}

func (p *Pool) complete(job *task) {
	p.mu.Lock()
	delete(p.pending, job.trip.TripID)
	p.mu.Unlock()
}
