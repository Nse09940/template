package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRequestPosition(t *testing.T) {
	t.Parallel()

	tripID, driverID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "accepted", status: http.StatusAccepted},
		{name: "client error", status: http.StatusTooManyRequests, want: ErrClientResponse},
		{name: "server error", status: http.StatusServiceUnavailable, want: ErrServerResponse},
		{name: "redirect", status: http.StatusTemporaryRedirect, want: ErrUnexpectedResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/push" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q", got)
				}
				if r.Header.Get("traceparent") == "" {
					t.Error("missing trace propagation")
				}
				var body requestPosition
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode push request: %v", err)
				}
				if body.RequestID == uuid.Nil || body.RecipientID != driverID ||
					body.Kind != "REQUEST_POSITION" || body.Data["trip_id"] != tripID {
					t.Errorf("unexpected push command: %+v", body)
				}
				w.Header().Set("Location", "/unexpected")
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			client, err := New(server.URL, time.Second, provider)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()

			ctx, parent := provider.Tracer("test").Start(t.Context(), "worker")
			defer parent.End()
			if err := client.RequestPosition(ctx, tripID, driverID); !errors.Is(err, tc.want) {
				t.Fatalf("RequestPosition() = %v, want %v", err, tc.want)
			}
			spans := recorder.Ended()
			if len(spans) != 1 || spans[0].SpanKind() != trace.SpanKindClient ||
				spans[0].Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Fatalf("expected one HTTP client span, got %v", spans)
			}
		})
	}
}

func TestRequestPositionTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	client, err := New(server.URL, 50*time.Millisecond, noop.NewTracerProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()

	err = client.RequestPosition(t.Context(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrTransport) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RequestPosition() = %v, want transport deadline error", err)
	}
}
