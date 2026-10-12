package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/trip"
	"github.com/google/uuid"
)

const maxRequestBodyBytes = 1 << 20

type Service interface {
	Create(ctx context.Context, input trip.CreateInput) (trip.CreateResult, error)
	Get(ctx context.Context, id uuid.UUID) (trip.Trip, error)
	Finish(ctx context.Context, id uuid.UUID) (trip.Trip, error)
}

type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	service      Service
	database     Pinger
	queryTimeout time.Duration
	logger       *slog.Logger
}

func NewHandler(
	service Service,
	database Pinger,
	queryTimeout time.Duration,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		service:      service,
		database:     database,
		queryTimeout: queryTimeout,
		logger:       logger,
	}
}

func (h *Handler) CreateTrip(
	w http.ResponseWriter,
	r *http.Request,
	params api.CreateTripParams,
) {
	if err := requireJSONContentType(r); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		return
	}

	body, err := decodeTripData(w, r)
	if err != nil {
		h.writeProblem(
			w,
			r,
			http.StatusBadRequest,
			"invalid_request",
			"Invalid request",
			err.Error(),
		)
		return
	}
	if err := validateTripData(body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		return
	}

	result, err := h.service.Create(r.Context(), trip.CreateInput{
		UserID:         body.UserId,
		DriverID:       body.DriverId,
		IdempotencyKey: params.IdempotencyKey,
		StartPoint: trip.Coordinates{
			Latitude:  body.StartPoint.Latitude,
			Longitude: body.StartPoint.Longitude,
		},
		EndPoint: trip.Coordinates{
			Latitude:  body.EndPoint.Latitude,
			Longitude: body.EndPoint.Longitude,
		},
		Price: body.Price,
	})
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	} else {
		w.Header().Set("Location", "/api/v1/trips/"+result.Trip.ID.String())
	}
	h.writeJSON(w, status, toAPITrip(result.Trip))
}

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	found, err := h.service.Get(r.Context(), tripID)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, toAPITrip(found))
}

func (h *Handler) FinishTrip(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	finished, err := h.service.Finish(r.Context(), tripID)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, toAPITrip(finished))
}

func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.queryTimeout)
	defer cancel()

	if err := h.database.Ping(ctx); err != nil {
		h.writeJSON(w, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}

	h.writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (h *Handler) ParameterError(w http.ResponseWriter, r *http.Request, err error) {
	h.writeProblem(
		w,
		r,
		http.StatusBadRequest,
		"invalid_request",
		"Invalid request",
		err.Error(),
	)
}

func (h *Handler) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, trip.ErrNotFound):
		h.writeProblem(
			w,
			r,
			http.StatusNotFound,
			"trip_not_found",
			"Trip not found",
			"Trip does not exist",
		)
	case errors.Is(err, trip.ErrCompleted):
		h.writeProblem(
			w,
			r,
			http.StatusConflict,
			"trip_completed",
			"Trip completed",
			"Trip is already completed",
		)
	case errors.Is(err, trip.ErrDriverBusy):
		h.writeProblem(
			w,
			r,
			http.StatusConflict,
			"driver_busy",
			"Driver busy",
			"Driver already has an active trip",
		)
	case errors.Is(err, trip.ErrIdempotencyConflict):
		h.writeProblem(
			w,
			r,
			http.StatusConflict,
			"idempotency_conflict",
			"Idempotency conflict",
			"Idempotency-Key was already used with a different request body",
		)
	default:
		h.logger.ErrorContext(
			r.Context(),
			"request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
		h.writeProblem(
			w,
			r,
			http.StatusInternalServerError,
			"internal_error",
			"Internal server error",
			"An internal error occurred",
		)
	}
}

func (h *Handler) writeProblem(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	title string,
	detail string,
) {
	instance := r.URL.Path
	problemType := "https://tripgo.example/problems/" + problemSlug(code)
	problem := api.Problem{
		Type:     problemType,
		Title:    title,
		Status:   int32(status),
		Detail:   &detail,
		Instance: &instance,
		Code:     code,
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		h.logger.ErrorContext(r.Context(), "encode problem response", "error", err)
	}
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("encode JSON response", "error", err)
	}
}

func requireJSONContentType(r *http.Request) error {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		return errors.New("Content-Type must be application/json")
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}

	return nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Request body must contain exactly one JSON object")
	}
	return nil
}

func decodeTripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return api.TripData{}, err
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return api.TripData{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return api.TripData{}, errors.New("request body must be a JSON object")
	}
	if err := requireFields(fields, "user_id", "driver_id", "start_point", "end_point", "price"); err != nil {
		return api.TripData{}, err
	}
	if err := requireCoordinateFields(fields["start_point"], "start_point"); err != nil {
		return api.TripData{}, err
	}
	if err := requireCoordinateFields(fields["end_point"], "end_point"); err != nil {
		return api.TripData{}, err
	}

	var body api.TripData
	typedDecoder := json.NewDecoder(bytes.NewReader(raw))
	typedDecoder.DisallowUnknownFields()
	if err := typedDecoder.Decode(&body); err != nil {
		return api.TripData{}, err
	}

	return body, nil
}

func requireCoordinateFields(raw json.RawMessage, name string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return requireFields(fields, "latitude", "longitude")
}

func requireFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			return fmt.Errorf("required field %q is missing", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %q must not be null", name)
		}
	}
	return nil
}

func validateTripData(value api.TripData) error {
	if value.UserId == uuid.Nil {
		return errors.New("user_id must be a non-empty UUID")
	}
	if value.DriverId == uuid.Nil {
		return errors.New("driver_id must be a non-empty UUID")
	}
	if err := validateCoordinates("start_point", value.StartPoint); err != nil {
		return err
	}
	if err := validateCoordinates("end_point", value.EndPoint); err != nil {
		return err
	}
	if value.Price < 0 {
		return errors.New("price must not be negative")
	}

	return nil
}

func validateCoordinates(name string, value api.Coordinates) error {
	if value.Latitude < -90 || value.Latitude > 90 {
		return fmt.Errorf("%s.latitude must be between -90 and 90", name)
	}
	if value.Longitude < -180 || value.Longitude > 180 {
		return fmt.Errorf("%s.longitude must be between -180 and 180", name)
	}
	return nil
}

func toAPITrip(value trip.Trip) api.Trip {
	return api.Trip{
		Id:             value.ID,
		UserId:         value.UserID,
		DriverId:       value.DriverID,
		StartPoint:     api.Coordinates(value.StartPoint),
		EndPoint:       api.Coordinates(value.EndPoint),
		Price:          value.Price,
		Status:         api.TripStatus(value.Status),
		StartedAt:      value.StartedAt,
		FinishedAt:     value.FinishedAt,
		LastPositionAt: value.LastPositionAt,
	}
}

func problemSlug(code string) string {
	switch code {
	case "invalid_request":
		return "invalid-request"
	case "trip_not_found":
		return "trip-not-found"
	case "trip_completed":
		return "trip-completed"
	case "driver_busy":
		return "driver-busy"
	case "idempotency_conflict":
		return "idempotency-conflict"
	default:
		return "internal-error"
	}
}
