package model

import (
	"time"

	"github.com/google/uuid"
)

type Position struct {
	ID          int64
	TripID      uuid.UUID
	Coordinates Coordinates
	RecordedAt  time.Time
}

type StaleTrip struct {
	TripID   uuid.UUID
	DriverID uuid.UUID
}
