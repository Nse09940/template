package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

type (
	queryKey  struct{}
	queryInfo struct {
		started    time.Time
		operation  string
		collection string
	}
)

type Tracer struct {
	*otelpgx.Tracer
	duration metric.Float64Histogram
}

func NewTracer(tp trace.TracerProvider, mp metric.MeterProvider) (*Tracer, error) {
	duration, err := mp.Meter("trip-service/postgres").Float64Histogram("db.client.operation.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create database duration metric: %w", err)
	}
	return &Tracer{Tracer: otelpgx.NewTracer(otelpgx.WithTracerProvider(tp), otelpgx.WithMeterProvider(noop.NewMeterProvider()), otelpgx.WithDisableSQLStatementInAttributes(), otelpgx.WithDisableConnectionDetailsInAttributes()), duration: duration}, nil
}

func (t *Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx = t.Tracer.TraceQueryStart(ctx, conn, data)
	operation, collection := queryAttributes(data.SQL)
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("db.operation.name", operation), attribute.String("db.collection.name", collection))
	return context.WithValue(ctx, queryKey{}, queryInfo{started: time.Now(), operation: operation, collection: collection})
}

func (t *Tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	info, _ := ctx.Value(queryKey{}).(queryInfo)
	errorType := ""
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		errorType = "database_error"
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			errorType = pgErr.Code
		}
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", errorType))
	}
	t.duration.Record(ctx, time.Since(info.started).Seconds(), metric.WithAttributes(attribute.String("db.operation.name", info.operation), attribute.String("db.collection.name", info.collection), attribute.String("error.type", errorType)))
	t.Tracer.TraceQueryEnd(ctx, conn, data)
}

func queryAttributes(sql string) (string, string) {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "", ""
	}
	operation := strings.ToUpper(fields[0])
	collection := ""
	for i, field := range fields {
		switch strings.ToUpper(field) {
		case "FROM", "INTO", "UPDATE":
			if i+1 < len(fields) {
				candidate := strings.Trim(fields[i+1], `"`)
				switch candidate {
				case "trips", "trip_status_history", "trip_create_idempotency_keys":
					collection = candidate
				}
				return operation, collection
			}
		}
	}
	return operation, collection
}
