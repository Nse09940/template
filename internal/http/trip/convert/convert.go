package convert

import (
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/model"
)

func Trip(value model.Trip) api.Trip {
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
