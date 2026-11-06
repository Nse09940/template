package telemetry_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/Nse09940/template/internal/infra/telemetry"
)

func TestLoggerCorrelation(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := telemetry.NewLogger(&output, "debug")
	if err != nil {
		t.Fatal(err)
	}
	provider := trace.NewTracerProvider()
	t.Cleanup(func() {
		if err := provider.Shutdown(t.Context()); err != nil {
			t.Error(err)
		}
	})
	ctx, span := provider.Tracer("test").Start(t.Context(), "request")
	defer span.End()
	logger.With("operation", "create").WithGroup("payload").InfoContext(ctx, "created", "count", 1)
	var body map[string]any
	if err := json.NewDecoder(&output).Decode(&body); err != nil {
		t.Fatal(err)
	}
	sc := span.SpanContext()
	if body["trace_id"] != sc.TraceID().String() || body["span_id"] != sc.SpanID().String() || body["service"] != "trip-service" || body["operation"] != "create" {
		t.Fatalf("entry=%+v", body)
	}
	payload, ok := body["payload"].(map[string]any)
	if !ok || payload["count"] != float64(1) {
		t.Fatalf("payload=%v", body["payload"])
	}
}
