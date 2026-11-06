package trip

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
)

var (
	ErrTripNotFound        = errors.New("trip not found")
	ErrTripCompleted       = errors.New("trip already completed")
	ErrDriverBusy          = errors.New("driver already has an active trip")
	ErrIdempotencyConflict = errors.New("idempotency key was used with a different request")
)

type Coordinates struct {
	Latitude  float64
	Longitude float64
}

type Trip struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	DriverID       uuid.UUID
	StartPoint     Coordinates
	EndPoint       Coordinates
	Price          int64
	Status         Status
	StartedAt      time.Time
	FinishedAt     *time.Time
	LastPositionAt *time.Time
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + " " + e.Message
}

func (t Trip) Validate() error {
	if t.UserID == uuid.Nil {
		return &ValidationError{Field: "user_id", Message: "must be a non-empty UUID"}
	}
	if t.DriverID == uuid.Nil {
		return &ValidationError{Field: "driver_id", Message: "must be a non-empty UUID"}
	}
	if err := t.StartPoint.Validate("start_point"); err != nil {
		return err
	}
	if err := t.EndPoint.Validate("end_point"); err != nil {
		return err
	}
	if t.Price < 0 {
		return &ValidationError{Field: "price", Message: "must not be negative"}
	}
	return nil
}

func (p Coordinates) Validate(field string) error {
	if math.IsNaN(p.Latitude) || math.IsInf(p.Latitude, 0) || p.Latitude < -90 || p.Latitude > 90 {
		return &ValidationError{Field: field + ".latitude", Message: "must be between -90 and 90"}
	}
	if math.IsNaN(p.Longitude) || math.IsInf(p.Longitude, 0) || p.Longitude < -180 || p.Longitude > 180 {
		return &ValidationError{Field: field + ".longitude", Message: "must be between -180 and 180"}
	}
	return nil
}

func (t *Trip) Finish(at time.Time) error {
	if t.Status == StatusCompleted {
		return ErrTripCompleted
	}
	if t.Status != StatusActive {
		return fmt.Errorf("unexpected trip status %q", t.Status)
	}
	at = at.UTC().Truncate(time.Microsecond)
	t.Status = StatusCompleted
	t.FinishedAt = &at
	return nil
}
