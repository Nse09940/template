package trip_test

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nse09940/template/internal/domain/trip"
)

func TestFinish(t *testing.T) {
	t.Parallel()
	for _, status := range []trip.Status{trip.StatusActive, trip.StatusCompleted, "corrupt"} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			at := time.Date(2026, 8, 24, 18, 0, 0, 123456789, time.FixedZone("MSK", 10800))
			prior := at.Add(-time.Hour)
			value := trip.Trip{Status: status}
			if status == trip.StatusCompleted {
				value.FinishedAt = &prior
			}
			before := value
			err := value.Finish(at)
			if status == trip.StatusActive {
				if err != nil || value.Status != trip.StatusCompleted || value.FinishedAt == nil || *value.FinishedAt != at.UTC().Truncate(time.Microsecond) {
					t.Fatalf("Finish = %+v, %v", value, err)
				}
			} else {
				if err == nil || !reflect.DeepEqual(value, before) {
					t.Fatalf("failed Finish mutated trip: %+v, %v", value, err)
				}
				if status == trip.StatusCompleted && !errors.Is(err, trip.ErrTripCompleted) {
					t.Fatalf("error = %v", err)
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*trip.Trip)
		field  string
	}{
		{"valid", func(*trip.Trip) {}, ""},
		{"user", func(v *trip.Trip) { v.UserID = uuid.Nil }, "user_id"},
		{"driver", func(v *trip.Trip) { v.DriverID = uuid.Nil }, "driver_id"},
		{"price", func(v *trip.Trip) { v.Price = -1 }, "price"},
		{"latitude", func(v *trip.Trip) { v.StartPoint.Latitude = 91 }, "start_point.latitude"},
		{"longitude", func(v *trip.Trip) { v.EndPoint.Longitude = -181 }, "end_point.longitude"},
		{"nan", func(v *trip.Trip) { v.EndPoint.Latitude = math.NaN() }, "end_point.latitude"},
		{"infinity", func(v *trip.Trip) { v.StartPoint.Longitude = math.Inf(1) }, "start_point.longitude"},
		{"boundary", func(v *trip.Trip) { v.StartPoint = trip.Coordinates{Latitude: -90, Longitude: 180} }, ""},
		{"maximum latitude", func(v *trip.Trip) { v.EndPoint.Latitude = 90 }, ""},
		{"minimum longitude", func(v *trip.Trip) { v.EndPoint.Longitude = -180 }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := trip.Trip{UserID: uuid.New(), DriverID: uuid.New()}
			tt.change(&v)
			err := v.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var validation *trip.ValidationError
			if !errors.As(err, &validation) || validation.Field != tt.field {
				t.Fatalf("Validate = %v, want %s", err, tt.field)
			}
		})
	}
}

func TestValidationErrorMessage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		err  *trip.ValidationError
		want string
	}{
		{
			name: "field-specific error",
			err:  &trip.ValidationError{Field: "start_point.latitude", Message: "must be between -90 and 90"},
			want: "start_point.latitude must be between -90 and 90",
		},
		{
			name: "request-wide error",
			err:  &trip.ValidationError{Message: "request body must be a JSON object"},
			want: "request body must be a JSON object",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
