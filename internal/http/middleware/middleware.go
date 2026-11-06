package middleware

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/Nse09940/template/internal/http/requeststate"
	"github.com/Nse09940/template/internal/http/respond"
)

type requestKey struct{}

type requestData struct {
	response   *responseBuffer
	state      *requeststate.State
	panicStack []byte
}

type responseBuffer struct {
	header http.Header
	body   bytes.Buffer
	status int
}

// Wrap composes recovery, tracing, request logging, metrics, and the router.
func Wrap(router chi.Router, logger *slog.Logger, tp trace.TracerProvider, mp metric.MeterProvider) (http.Handler, error) {
	meter := mp.Meter("trip-service/http")
	duration, err := meter.Float64Histogram("http.server.request.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create HTTP duration histogram: %w", err)
	}
	active, err := meter.Int64UpDownCounter("http.server.active_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create HTTP active requests counter: %w", err)
	}

	measured := metrics(router, duration, active)
	logged := logging(measured, logger)
	traced := otelhttp.NewHandler(logged, "HTTP request",
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithMeterProvider(noop.NewMeterProvider()),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.Pattern
		}),
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = routePattern(router, r)
		ctx, state := requeststate.With(r.Context(), r)
		data := &requestData{response: &responseBuffer{header: w.Header().Clone()}, state: state}
		r = r.WithContext(context.WithValue(ctx, requestKey{}, data))

		defer func() {
			if recovered := recover(); recovered != nil {
				data.response.reset()
				failure := fmt.Errorf("HTTP handler panic: %v", recovered)
				respond.Writer{Logger: logger}.Error(data.response, state.Request, failure)
				logger.ErrorContext(state.Request.Context(), "panic recovered", "error", failure,
					"stack", string(data.panicStack))
			}
			for name, values := range data.response.header {
				w.Header()[name] = values
			}
			w.WriteHeader(data.response.statusCode())
			if _, err := w.Write(data.response.body.Bytes()); err != nil {
				logger.ErrorContext(state.Request.Context(), "write HTTP response", "error", err)
			}
		}()
		traced.ServeHTTP(data.response, r)
	}), nil
}

func routePattern(router chi.Router, r *http.Request) string {
	path := r.URL.Path
	if r.URL.RawPath != "" {
		path = r.URL.RawPath
	}
	match := chi.NewRouteContext()
	if !router.Match(match, r.Method, path) {
		return "unmatched"
	}
	return match.RoutePattern()
}

func logging(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := r.Context().Value(requestKey{}).(*requestData)
		data.state.Request = r
		started := time.Now()
		defer func() {
			status := data.response.statusCode()
			level := slog.LevelInfo
			if status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			attrs := []any{
				"method", r.Method, "route", r.Pattern, "status", status,
				"duration_ms", float64(time.Since(started)) / float64(time.Millisecond),
			}
			if data.state.Err != nil && len(data.panicStack) == 0 {
				attrs = append(attrs, "error", data.state.Err)
			}
			logger.Log(r.Context(), level, "request handled", attrs...)
		}()
		next.ServeHTTP(w, r)
	})
}

func metrics(next http.Handler, duration metric.Float64Histogram, active metric.Int64UpDownCounter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := r.Context().Value(requestKey{}).(*requestData)
		attrs := []attribute.KeyValue{
			attribute.String("http.request.method", r.Method),
			attribute.String("http.route", r.Pattern),
		}
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attrs...)
		active.Add(r.Context(), 1, metric.WithAttributes(attrs...))
		started := time.Now()
		defer func() {
			recovered := recover()
			if recovered != nil {
				data.response.status = http.StatusInternalServerError
				data.panicStack = debug.Stack()
				span.SetStatus(codes.Error, "internal_error")
				span.SetAttributes(attribute.String("error.type", "internal_error"))
				span.RecordError(fmt.Errorf("HTTP handler panic: %v", recovered))
			}
			status := data.response.statusCode()
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			active.Add(r.Context(), -1, metric.WithAttributes(attrs...))
			attrs = append(attrs, attribute.Int("http.response.status_code", status))
			duration.Record(r.Context(), time.Since(started).Seconds(), metric.WithAttributes(attrs...))
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (w *responseBuffer) Header() http.Header {
	return w.header
}

func (w *responseBuffer) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *responseBuffer) Write(body []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(body)
}

func (w *responseBuffer) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *responseBuffer) reset() {
	w.header = make(http.Header)
	w.body.Reset()
	w.status = 0
}
