package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"go.opentelemetry.io/otel/trace"
)

// NewLogger correlates every context-aware log record with its current span.
func NewLogger(output io.Writer, levelText string) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelText)); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL: %w", err)
	}
	handler := &contextHandler{
		base: slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}),
	}
	service := os.Getenv("OTEL_SERVICE_NAME")
	if service == "" {
		service = "trip-service"
	}
	return slog.New(handler).With("service", service), nil
}

type handlerOperation struct {
	attrs []slog.Attr
	group string
}

type contextHandler struct {
	base       slog.Handler
	operations []handlerOperation
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	handler := h.base
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		handler = handler.WithAttrs([]slog.Attr{
			slog.String("trace_id", span.TraceID().String()),
			slog.String("span_id", span.SpanID().String()),
		})
	}
	// Apply groups after correlation so trace IDs remain top-level JSON fields.
	for _, operation := range h.operations {
		if operation.group != "" {
			handler = handler.WithGroup(operation.group)
		} else {
			handler = handler.WithAttrs(operation.attrs)
		}
	}
	return handler.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	operations := append(slices.Clone(h.operations), handlerOperation{attrs: slices.Clone(attrs)})
	return &contextHandler{base: h.base, operations: operations}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	operations := append(slices.Clone(h.operations), handlerOperation{group: name})
	return &contextHandler{base: h.base, operations: operations}
}
