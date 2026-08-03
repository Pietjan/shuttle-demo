package desk

import (
	"context"
	"testing"
	"time"
)

func TestAddCommentMarksFirstResponse(t *testing.T) {
	t.Parallel()

	store := NewMemory()
	openedAt := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return openedAt }

	ticket, err := store.CreateTicket(context.Background(), Ticket{
		Subject:  "Need help",
		Customer: "Acme",
		Priority: PriorityHigh,
	}, "a-nadia")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	first := openedAt.Add(25 * time.Minute)
	store.now = func() time.Time { return first }
	if _, err := store.AddComment(context.Background(), Comment{
		TicketID: ticket.ID,
		AuthorID: "a-nadia",
		Body:     "First response",
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	got, err := store.Ticket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	if got.FirstResponseAt.IsZero() {
		t.Fatal("FirstResponseAt was not set")
	}
	if !got.FirstResponseAt.Equal(first) {
		t.Fatalf("FirstResponseAt = %v, want %v", got.FirstResponseAt, first)
	}
}

func TestResolutionBreachEscalatesOnRead(t *testing.T) {
	t.Parallel()

	store := NewMemory()
	openedAt := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return openedAt }

	ticket, err := store.CreateTicket(context.Background(), Ticket{
		Subject:  "Payments broken",
		Customer: "Acme",
		Priority: PriorityCritical,
	}, "a-ravi")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	store.now = func() time.Time { return openedAt.Add(5 * time.Hour) }
	got, err := store.Ticket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	if got.EscalatedAt.IsZero() {
		t.Fatal("ticket did not escalate after breached resolution SLA")
	}

	events, err := store.Events(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events.Rows) == 0 {
		t.Fatal("no events were recorded")
	}
	if events.Rows[0].Kind != EventEscalated {
		t.Fatalf("latest event kind = %q, want %q", events.Rows[0].Kind, EventEscalated)
	}
}
