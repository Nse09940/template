package postgres

import (
	"testing"
	"time"

	"github.com/Nse09940/template/internal/trip"
	"github.com/google/uuid"
)

type stubTripRow struct {
	startedAt  time.Time
	finishedAt *time.Time
}

func (r stubTripRow) Scan(dest ...any) error {
	*dest[0].(*uuid.UUID) = uuid.New()
	*dest[1].(*uuid.UUID) = uuid.New()
	*dest[2].(*uuid.UUID) = uuid.New()
	*dest[3].(*float64) = 59.9
	*dest[4].(*float64) = 30.3
	*dest[5].(*float64) = 59.8
	*dest[6].(*float64) = 30.4
	*dest[7].(*int64) = 1450
	*dest[8].(*string) = string(trip.StatusCompleted)
	*dest[9].(*time.Time) = r.startedAt
	*dest[10].(**time.Time) = r.finishedAt
	return nil
}

func TestScanTripNormalizesTimestampsToUTC(t *testing.T) {
	t.Parallel()

	moscow := time.FixedZone("Europe/Moscow", 3*60*60)
	startedAt := time.Date(2026, 8, 24, 17, 18, 27, 905923000, moscow)
	finishedAt := startedAt.Add(time.Hour)

	got, err := scanTrip(stubTripRow{startedAt: startedAt, finishedAt: &finishedAt})
	if err != nil {
		t.Fatalf("scanTrip: %v", err)
	}
	if got.StartedAt != startedAt.UTC() {
		t.Fatalf("StartedAt = %s, want %s", got.StartedAt, startedAt.UTC())
	}
	if got.FinishedAt == nil || *got.FinishedAt != finishedAt.UTC() {
		t.Fatalf("FinishedAt = %v, want %s", got.FinishedAt, finishedAt.UTC())
	}
}
