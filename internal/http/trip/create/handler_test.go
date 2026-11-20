package create_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/Nse09940/template/internal/domain/trip"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/http/trip/create"
	"github.com/Nse09940/template/internal/model"
)

const validBody = `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c","driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5","start_point":{"latitude":59.9,"longitude":30.3},"end_point":{"latitude":59.8,"longitude":30.4},"price":1450}`

func TestCreateHandler(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		body   string
		err    error
		status int
		replay bool
		code   string
	}{
		{"success", validBody, nil, 201, false, ""},
		{"replay", validBody, nil, 200, true, ""},
		{"busy", validBody, trip.ErrDriverBusy, 409, false, "driver_busy"},
		{"internal", validBody, errors.New("database password=secret"), 500, false, "internal_error"},
		{"invalid", "{}", nil, 400, false, "invalid_request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := NewMockusecase(gomock.NewController(t))
			id := uuid.New()
			key := uuid.New()
			if tt.status != 400 {
				u.EXPECT().Create(gomock.Any(), gomock.Cond(func(input model.CreateInput) bool {
					return input.Price == 1450 && input.IdempotencyKey != nil && *input.IdempotencyKey == key
				})).Return(model.CreateResult{Trip: model.Trip{ID: id, Status: trip.StatusActive}, Replayed: tt.replay}, tt.err)
			}
			h := create.New(u, slog.New(slog.NewTextHandler(io.Discard, nil)))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			h.CreateTrip(res, req, api.CreateTripParams{IdempotencyKey: &key})
			if res.Code != tt.status {
				t.Fatalf("status=%d body=%s", res.Code, res.Body)
			}
			if tt.code != "" {
				var body api.Problem
				if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Code != tt.code || strings.Contains(*body.Detail, "secret") {
					t.Fatalf("body=%+v", body)
				}
				return
			}
			var body api.Trip
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Id != id {
				t.Fatal("wrong trip")
			}
			if !tt.replay && res.Header().Get("Location") != "/api/v1/trips/"+id.String() {
				t.Fatal("missing Location")
			}
		})
	}
}

func TestCreateTripRejectsInvalidBodies(t *testing.T) {
	t.Parallel()

	valid := `"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
		"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
		"start_point":{"latitude":59.9,"longitude":30.3},
		"end_point":{"latitude":59.8,"longitude":30.4},
		"price":1450`

	tests := map[string]struct {
		body       string
		wantDetail string
	}{
		"missing required field": {
			body: `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
			"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
			"start_point":{"latitude":59.9,"longitude":30.3},
			"price":1450}`,
			wantDetail: `required field "end_point" is missing`,
		},
		"missing nested field": {
			body: `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
			"driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
			"start_point":{"latitude":59.9},
			"end_point":{"latitude":59.8,"longitude":30.4},
			"price":1450}`,
			wantDetail: `required field "longitude" is missing`,
		},
		"unknown field": {
			body:       "{" + valid + `,"unexpected":true}`,
			wantDetail: `json: unknown field "unexpected"`,
		},
		"out of range latitude": {
			body:       "{" + strings.Replace(valid, `"latitude":59.9`, `"latitude":91`, 1) + "}",
			wantDetail: "start_point.latitude must be between -90 and 90",
		},
		"null price": {
			body:       "{" + strings.Replace(valid, `"price":1450`, `"price":null`, 1) + "}",
			wantDetail: `required field "price" must not be null`,
		},
		"null latitude": {
			body:       "{" + strings.Replace(valid, `"latitude":59.9`, `"latitude":null`, 1) + "}",
			wantDetail: `required field "latitude" must not be null`,
		},
		"null longitude": {
			body:       "{" + strings.Replace(valid, `"longitude":30.3`, `"longitude":null`, 1) + "}",
			wantDetail: `required field "longitude" must not be null`,
		},
		"multiple values": {
			body:       "{" + valid + `} {}`,
			wantDetail: "Request body must contain exactly one JSON object",
		},
	}

	handler := create.New(NewMockusecase(gomock.NewController(t)), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(test.body))
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
			if problem.Detail == nil || *problem.Detail != test.wantDetail {
				t.Fatalf("problem detail = %v, want %q", problem.Detail, test.wantDetail)
			}
		})
	}
}
