package desk

import (
	"context"
	"testing"
	"time"
)

func TestStatsReportsMTTR(t *testing.T) {
	t.Parallel()

	store := NewMemory()
	openedAt := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return openedAt }

	ticket, err := store.CreateTicket(context.Background(), Ticket{Subject: "Slow ticket", Customer: "A"}, "a-one")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	store.now = func() time.Time { return openedAt.Add(3 * time.Hour) }
	if _, err := store.SetStatus(context.Background(), ticket.ID, StatusResolved, "a-one"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.MTTR != 3*time.Hour {
		t.Fatalf("MTTR = %v, want %v", stats.MTTR, 3*time.Hour)
	}
}
