package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/http/middleware"
	"github.com/Nse09940/template/internal/http/respond"
)

const (
	tripRoute      = "/api/v1/trips/{tripId}"
	privateFailure = "private database detail"
	requestPath    = "/api/v1/trips/010a9890-1d13-4ce3-a261-cbba9c44e123"
)

type harness struct {
	handler   http.Handler
	spans     *tracetest.InMemoryExporter
	reader    *sdkmetric.ManualReader
	logs      *bytes.Buffer
	responder respond.Writer
}

// The sink makes the injected logger's received span context observable.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	return h.Handler.Handle(ctx, record)
}

func newHarness(t *testing.T, router chi.Router) harness {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	logs := &bytes.Buffer{}
	logger := slog.New(contextHandler{Handler: slog.NewJSONHandler(logs, nil)})
	handler, err := middleware.Wrap(router, logger, tp, mp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := mp.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return harness{handler: handler, spans: spans, reader: reader, logs: logs, responder: respond.Writer{Logger: logger}}
}

func TestRequestSignals(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		failure   error
		panic     bool
		unmatched bool
	}{
		{name: "created", status: http.StatusCreated},
		{name: "business error", status: http.StatusConflict, failure: trip.ErrDriverBusy},
		{name: "repository error", status: http.StatusInternalServerError, failure: errors.New(privateFailure)},
		{name: "panic after partial response", status: http.StatusInternalServerError, panic: true},
		{name: "unknown route", status: http.StatusNotFound, unmatched: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := chi.NewRouter()
			var service harness
			router.Post(tripRoute, func(w http.ResponseWriter, r *http.Request) {
				if test.failure != nil {
					service.responder.Error(w, r, test.failure)
					return
				}
				w.WriteHeader(http.StatusCreated)
				if _, err := w.Write([]byte("private response content")); err != nil {
					t.Error(err)
				}
				if test.panic {
					panic(privateFailure)
				}
			})
			service = newHarness(t, router)
			path, route := requestPath, tripRoute
			if test.unmatched {
				path, route = "/unknown/private/path", "unmatched"
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("private request content"))
			request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			response := httptest.NewRecorder()
			service.handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.status >= http.StatusInternalServerError {
				if strings.Contains(response.Body.String(), privateFailure) || strings.Contains(response.Body.String(), "private response content") {
					t.Fatalf("internal details escaped in body %q", response.Body.String())
				}
				var problem struct{ Code string }
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != "internal_error" {
					t.Fatalf("invalid problem response %q, error %v", response.Body.String(), err)
				}
			}
			spans := service.spans.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			span := spans[0]
			if span.Name != "POST "+route || span.SpanKind != trace.SpanKindServer {
				t.Fatalf("span name/kind = %q/%v", span.Name, span.SpanKind)
			}
			if span.SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || !span.Parent.IsRemote() {
				t.Fatal("incoming W3C trace context was lost")
			}
			if (test.failure != nil || test.panic) && span.Status.Code != codes.Error {
				t.Fatalf("error span status = %v", span.Status.Code)
			}
			attributes := attribute.NewSet(span.Attributes...)
			assertAttribute(t, attributes, "http.route", route)
			if test.failure != nil || test.panic {
				value, ok := attributes.Value("error.type")
				if !ok || value.AsString() == "" {
					t.Fatal("error span has no error.type")
				}
			}
			assertRequestLogs(t, service.logs, span, route, test.status, test.panic)
			assertMetrics(t, service.reader, route, test.status, 0)
		})
	}
}

func assertRequestLogs(t *testing.T, logs *bytes.Buffer, span tracetest.SpanStub, route string, status int, panicked bool) {
	t.Helper()
	if strings.Contains(logs.String(), "private request content") || strings.Contains(logs.String(), "private response content") {
		t.Fatal("request or response body was logged")
	}
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	requestLogs, panicLogs := 0, 0
	for decoder.More() {
		var record struct {
			Msg     string
			Level   string
			Route   string
			Status  int
			Stack   string
			TraceID string `json:"trace_id"`
			SpanID  string `json:"span_id"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record.TraceID != span.SpanContext.TraceID().String() || record.SpanID != span.SpanContext.SpanID().String() {
			t.Fatal("log record does not share the request span context")
		}
		switch record.Msg {
		case "request handled":
			requestLogs++
			level := "INFO"
			if status >= http.StatusInternalServerError {
				level = "ERROR"
			}
			if record.Status != status || record.Route != route || record.Level != level {
				t.Fatalf("request log = %+v", record)
			}
		case "panic recovered":
			panicLogs++
			if record.Stack == "" || record.Level != "ERROR" {
				t.Fatal("recovered panic lacks error level or stack trace")
			}
		}
	}
	if requestLogs != 1 || (panicked && panicLogs != 1) || (!panicked && panicLogs != 0) {
		t.Fatalf("request logs = %d, panic logs = %d", requestLogs, panicLogs)
	}
}

func assertAttribute(t *testing.T, attributes attribute.Set, key attribute.Key, want string) {
	t.Helper()
	value, ok := attributes.Value(key)
	if !ok || value.AsString() != want {
		t.Fatalf("attribute %s = %v, want %q", key, value, want)
	}
}

func assertMetrics(t *testing.T, reader *sdkmetric.ManualReader, route string, status int, active int64) {
	t.Helper()
	var result metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	seenActive, seenDuration := false, false
	for _, scope := range result.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch measurement.Name {
			case "http.server.active_requests":
				seenActive = true
				sum, ok := measurement.Data.(metricdata.Sum[int64])
				if !ok || sum.IsMonotonic || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != active {
					t.Fatalf("active requests = %+v, want %d", measurement.Data, active)
				}
				assertAttribute(t, sum.DataPoints[0].Attributes, "http.request.method", http.MethodPost)
				assertAttribute(t, sum.DataPoints[0].Attributes, "http.route", route)
			case "http.server.request.duration":
				seenDuration = true
				histogram, ok := measurement.Data.(metricdata.Histogram[float64])
				if !ok || measurement.Unit != "s" || len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
					t.Fatalf("request duration = %+v", measurement)
				}
				point := histogram.DataPoints[0]
				assertAttribute(t, point.Attributes, "http.request.method", http.MethodPost)
				assertAttribute(t, point.Attributes, "http.route", route)
				value, ok := point.Attributes.Value("http.response.status_code")
				if !ok || value.AsInt64() != int64(status) {
					t.Fatalf("duration status attribute = %v", value)
				}
			}
		}
	}
	if !seenActive || (active == 0 && !seenDuration) {
		t.Fatalf("missing metrics, active=%v duration=%v", seenActive, seenDuration)
	}
}

func TestActiveRequestsMeasuredWhileHandlerRuns(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	router := chi.NewRouter()
	router.Post(tripRoute, func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusCreated)
	})
	service := newHarness(t, router)
	go func() {
		defer close(finished)
		service.handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, requestPath, nil))
	}()
	<-entered
	t.Run("in flight", func(t *testing.T) {
		assertMetrics(t, service.reader, tripRoute, http.StatusCreated, 1)
	})
	close(release)
	<-finished
	assertMetrics(t, service.reader, tripRoute, http.StatusCreated, 0)
}
