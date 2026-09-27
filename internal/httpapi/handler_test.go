package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/trip"
	"github.com/google/uuid"
)

type stubService struct {
	create func(context.Context, trip.CreateInput) (trip.CreateResult, error)
	get    func(context.Context, uuid.UUID) (trip.Trip, error)
	finish func(context.Context, uuid.UUID) (trip.Trip, error)
}

func (s stubService) Create(ctx context.Context, input trip.CreateInput) (trip.CreateResult, error) {
	if s.create == nil {
		return trip.CreateResult{}, errors.New("unexpected Create call")
	}
	return s.create(ctx, input)
}

func (s stubService) Get(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	if s.get == nil {
		return trip.Trip{}, errors.New("unexpected Get call")
	}
	return s.get(ctx, id)
}

func (s stubService) Finish(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	if s.finish == nil {
		return trip.Trip{}, errors.New("unexpected Finish call")
	}
	return s.finish(ctx, id)
}

type stubPinger struct {
	err   error
	calls int
}

func (p *stubPinger) Ping(context.Context) error {
	p.calls++
	return p.err
}

func newTestHandler(service Service, pinger Pinger) *Handler {
	return NewHandler(
		service,
		pinger,
		time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func TestCreateTripRejectsInvalidBodies(t *testing.T) {
	t.Parallel()

	valid := `"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
		"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
		"start_point":{"latitude":59.9,"longitude":30.3},
		"end_point":{"latitude":59.8,"longitude":30.4},
		"price":1450`

	tests := map[string]string{
		"missing required field": `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
			"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
			"start_point":{"latitude":59.9,"longitude":30.3},
			"price":1450}`,
		"missing nested field": `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
			"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
			"start_point":{"latitude":59.9},
			"end_point":{"latitude":59.8,"longitude":30.4},
			"price":1450}`,
		"unknown field":         "{" + valid + `,"unexpected":true}`,
		"out of range latitude": "{" + strings.Replace(valid, `"latitude":59.9`, `"latitude":91`, 1) + "}",
		"null price":            "{" + strings.Replace(valid, `"price":1450`, `"price":null`, 1) + "}",
		"null latitude":         "{" + strings.Replace(valid, `"latitude":59.9`, `"latitude":null`, 1) + "}",
		"null longitude":        "{" + strings.Replace(valid, `"longitude":30.3`, `"longitude":null`, 1) + "}",
		"multiple values":       "{" + valid + `} {}`,
	}

	handler := newTestHandler(stubService{}, &stubPinger{})
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.CreateTrip(response, request, api.CreateTripParams{})

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("Content-Type = %q", got)
			}

			var problem api.Problem
			if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != "invalid_request" {
				t.Fatalf("problem code = %q", problem.Code)
			}
		})
	}
}

func TestCreateTripReturnsCreatedTrip(t *testing.T) {
	t.Parallel()

	tripID := uuid.MustParse("1f0a9c62-4a1c-4f2e-9d33-2a4bb0f0b111")
	userID := uuid.MustParse("5cb72c04-7650-45c9-a79b-bcdba0631e0c")
	driverID := uuid.MustParse("8860b315-ec86-42eb-a17c-7c163d721ff5")
	startedAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	service := stubService{
		create: func(_ context.Context, input trip.CreateInput) (trip.CreateResult, error) {
			if input.UserID != userID || input.DriverID != driverID || input.Price != 1450 {
				t.Fatalf("unexpected input: %+v", input)
			}
			return trip.CreateResult{
				Trip: trip.Trip{
					ID:         tripID,
					UserID:     userID,
					DriverID:   driverID,
					StartPoint: input.StartPoint,
					EndPoint:   input.EndPoint,
					Price:      input.Price,
					Status:     trip.StatusActive,
					StartedAt:  startedAt,
				},
			}, nil
		},
	}
	handler := newTestHandler(service, &stubPinger{})

	body := `{
		"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
		"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
		"start_point":{"latitude":59.9398,"longitude":30.3146},
		"end_point":{"latitude":59.929,"longitude":30.3626},
		"price":1450
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.CreateTrip(response, request, api.CreateTripParams{})

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Location"); got != "/api/v1/trips/"+tripID.String() {
		t.Fatalf("Location = %q", got)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}

	var result api.Trip
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode trip: %v", err)
	}
	if result.Id != tripID || result.Status != api.Active {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestCreateTripReplayReturnsOK(t *testing.T) {
	t.Parallel()

	tripID := uuid.MustParse("1f0a9c62-4a1c-4f2e-9d33-2a4bb0f0b111")
	idempotencyKey := uuid.MustParse("7f1c2a54-6d0e-4a3b-9f21-0c8e5b3a77d1")
	service := stubService{
		create: func(_ context.Context, input trip.CreateInput) (trip.CreateResult, error) {
			if input.IdempotencyKey == nil || *input.IdempotencyKey != idempotencyKey {
				t.Fatalf("idempotency key = %v", input.IdempotencyKey)
			}
			return trip.CreateResult{
				Trip: trip.Trip{
					ID:        tripID,
					UserID:    input.UserID,
					DriverID:  input.DriverID,
					Price:     input.Price,
					Status:    trip.StatusActive,
					StartedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
				},
				Replayed: true,
			}, nil
		},
	}
	handler := newTestHandler(service, &stubPinger{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(`{
		"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
		"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
		"start_point":{"latitude":59.9398,"longitude":30.3146},
		"end_point":{"latitude":59.929,"longitude":30.3626},
		"price":1450
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.CreateTrip(response, request, api.CreateTripParams{IdempotencyKey: &idempotencyKey})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "" {
		t.Fatalf("Location = %q, want empty", location)
	}
}

func TestCreateTripMapsIdempotencyConflict(t *testing.T) {
	t.Parallel()

	service := stubService{
		create: func(context.Context, trip.CreateInput) (trip.CreateResult, error) {
			return trip.CreateResult{}, trip.ErrIdempotencyConflict
		},
	}
	handler := newTestHandler(service, &stubPinger{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(`{
		"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
		"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
		"start_point":{"latitude":59.9398,"longitude":30.3146},
		"end_point":{"latitude":59.929,"longitude":30.3626},
		"price":1450
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.CreateTrip(response, request, api.CreateTripParams{})

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	var problem api.Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != "idempotency_conflict" {
		t.Fatalf("problem code = %q", problem.Code)
	}
}

func TestGeneratedRouterMapsInvalidTripIDToProblem(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(stubService{}, &stubPinger{})
	router := api.HandlerWithOptions(handler, api.ChiServerOptions{
		ErrorHandlerFunc: handler.ParameterError,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/trips/not-a-uuid", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestHealthDoesNotPingDatabaseAndReadyDoes(t *testing.T) {
	t.Parallel()

	pinger := &stubPinger{err: errors.New("database unavailable")}
	handler := newTestHandler(stubService{}, pinger)

	healthResponse := httptest.NewRecorder()
	handler.Health(healthResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if healthResponse.Code != http.StatusOK || pinger.calls != 0 {
		t.Fatalf("health status = %d, ping calls = %d", healthResponse.Code, pinger.calls)
	}

	readyResponse := httptest.NewRecorder()
	handler.Ready(readyResponse, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if readyResponse.Code != http.StatusServiceUnavailable || pinger.calls != 1 {
		t.Fatalf("ready status = %d, ping calls = %d", readyResponse.Code, pinger.calls)
	}
}
