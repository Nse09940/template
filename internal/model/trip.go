package model

import (
	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/domain/trip"
)

type (
	Trip        = trip.Trip
	Coordinates = trip.Coordinates
)

type CreateInput struct {
	UserID         uuid.UUID
	DriverID       uuid.UUID
	StartPoint     Coordinates
	EndPoint       Coordinates
	Price          int64
	IdempotencyKey *uuid.UUID
}

type CreateResult struct {
	Trip     Trip
	Replayed bool
}

type CreateIdempotency struct {
	RequestHash []byte
	TripID      uuid.UUID
}
