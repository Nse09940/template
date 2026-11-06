package http_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/generated"
	transport "github.com/Nse09940/template/internal/http"
)

func TestInvalidPathParameter(t *testing.T) {
	t.Parallel()
	handler := transport.NewHandler(nil, nil, nil, NewMockpinger(gomock.NewController(t)), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := api.HandlerWithOptions(handler, api.ChiServerOptions{ErrorHandlerFunc: handler.ParameterError})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/trips/not-a-uuid", nil)
	router.ServeHTTP(res, req)
	if res.Code != 400 || res.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(res.Body.String(), "invalid_request") || !strings.Contains(res.Body.String(), "tripId") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "unavailable"}[unavailable], func(t *testing.T) {
			t.Parallel()
			db := NewMockpinger(gomock.NewController(t))
			failure := error(nil)
			expected := 200
			if unavailable {
				failure = errors.New("connection refused")
				expected = 503
			}
			handler := transport.NewHandler(nil, nil, nil, db, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
			res := httptest.NewRecorder()
			handler.Health(res, httptest.NewRequest(http.MethodGet, "/health", nil))
			if res.Code != 200 {
				t.Fatalf("health=%d", res.Code)
			}
			db.EXPECT().Ping(gomock.Any()).Return(failure)
			res = httptest.NewRecorder()
			handler.Ready(res, httptest.NewRequest(http.MethodGet, "/ready", nil))
			if res.Code != expected {
				t.Fatalf("ready=%d", res.Code)
			}
		})
	}
}
