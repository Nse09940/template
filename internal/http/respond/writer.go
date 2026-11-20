package respond

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Nse09940/template/internal/http/domainerror"
	"github.com/Nse09940/template/internal/http/requeststate"
)

type Writer struct{ Logger *slog.Logger }

func (w Writer) JSON(dst http.ResponseWriter, r *http.Request, status int, value any) {
	dst.Header().Set("Content-Type", "application/json")
	dst.WriteHeader(status)
	if err := json.NewEncoder(dst).Encode(value); err != nil {
		w.Logger.ErrorContext(r.Context(), "encode JSON response", "error", err)
	}
}

func (w Writer) Error(dst http.ResponseWriter, r *http.Request, err error) {
	status, problem := domainerror.Problem(err)
	path := r.URL.Path
	problem.Instance = &path
	span := trace.SpanFromContext(r.Context())
	span.SetAttributes(attribute.String("error.type", problem.Code))
	span.SetStatus(codes.Error, problem.Code)
	if status >= 500 {
		span.RecordError(err)
		requeststate.SetError(r.Context(), err)
	}
	dst.Header().Set("Content-Type", "application/problem+json")
	dst.WriteHeader(status)
	if err := json.NewEncoder(dst).Encode(problem); err != nil {
		w.Logger.ErrorContext(r.Context(), "encode problem response", "error", err)
	}
}
