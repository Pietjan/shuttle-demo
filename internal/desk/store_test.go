package desk_test

import (
	"context"
	"sync"
	"testing"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// fixture builds a store with three tickets in a known shape.
func fixture(t *testing.T) *desk.Memory {
	t.Helper()

	store := desk.NewMemory()
	desk.Seed(store)
	return store
}

func TestTicketsFilterSortPage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := fixture(t)

	all, err := store.Tickets(ctx, desk.Filter{})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	if all.Total == 0 {
		t.Fatal("the seed produced no tickets")
	}

	t.Run("a page is a page, and Total is the whole match", func(t *testing.T) {
		page, err := store.Tickets(ctx, desk.Filter{Limit: 5})
		if err != nil {
			t.Fatalf("Tickets: %v", err)
		}
		if len(page.Rows) != 5 {
			t.Errorf("got %d rows, want 5", len(page.Rows))
		}
		if page.Total != all.Total {
			t.Errorf("Total = %d, want %d - a page must not shrink the count the pager reads", page.Total, all.Total)
		}
	})

	t.Run("an offset past the end is empty, not an error", func(t *testing.T) {
		// The case a stale page= in somebody's bookmarked URL produces.
		page, err := store.Tickets(ctx, desk.Filter{Offset: all.Total + 100, Limit: 5})
		if err != nil {
			t.Fatalf("Tickets: %v", err)
		}
		if len(page.Rows) != 0 {
			t.Errorf("got %d rows, want none", len(page.Rows))
		}
		if page.Total != all.Total {
			t.Errorf("Total = %d, want %d", page.Total, all.Total)
		}
	})

	t.Run("unassigned is its own question", func(t *testing.T) {
		// The sentinel exists because "" already means "any assignee", and a
		// queue has to be able to ask both.
		page, err := store.Tickets(ctx, desk.Filter{Assignee: desk.Unassigned})
		if err != nil {
			t.Fatalf("Tickets: %v", err)
		}
		if page.Total == 0 {
			t.Fatal("no unassigned tickets in the seed - the queue would have nothing to claim")
		}
		for _, ticket := range page.Rows {
			if !ticket.Unassigned() {
				t.Errorf("%s is assigned to %q but matched the unassigned filter", ticket.ID, ticket.Assignee)
			}
		}
	})

	t.Run("priority sorts by rank, not alphabetically", func(t *testing.T) {
		// "critical" < "high" < "low" < "normal" as strings, which would put
		// the most urgent tickets in the middle of the queue.
		page, err := store.Tickets(ctx, desk.Filter{Sort: "priority", Desc: true, Limit: 4})
		if err != nil {
			t.Fatalf("Tickets: %v", err)
		}
		if len(page.Rows) == 0 {
			t.Fatal("no rows")
		}
		if got := page.Rows[0].Priority; got != desk.PriorityLow {
			t.Errorf("first row descending = %q, want %q", got, desk.PriorityLow)
		}
	})

	t.Run("an unknown sort key orders rather than empties", func(t *testing.T) {
		page, err := store.Tickets(ctx, desk.Filter{Sort: "nonsense", Limit: 3})
		if err != nil {
			t.Fatalf("Tickets: %v", err)
		}
		if len(page.Rows) != 3 {
			t.Errorf("got %d rows, want 3 - a stale sort in a bookmark must not empty the queue", len(page.Rows))
		}
	})

	t.Run("query matches the columns the table shows", func(t *testing.T) {
		page, err := store.Tickets(ctx, desk.Filter{Query: "northwind"})
		if err != nil {
			t.Fatalf("Tickets: %v", err)
		}
		if page.Total == 0 {
			t.Fatal("filtering by a customer name found nothing")
		}
	})
}

func TestTicketRefsDoNotSkip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := desk.NewMemory()

	// Filing a ticket also writes an event, so a single shared id counter
	// produces T-1, T-3, T-5 - and a desk whose refs skip looks like a desk
	// that is losing tickets.
	for _, want := range []string{"T-1", "T-2", "T-3"} {
		ticket, err := store.CreateTicket(ctx, desk.Ticket{Subject: "s", Customer: "c"}, "a")
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		if ticket.ID != want {
			t.Errorf("ticket id = %q, want %q", ticket.ID, want)
		}
	}
}

func TestAssignAndStatusRecordEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := desk.NewMemory()

	ticket, err := store.CreateTicket(ctx, desk.Ticket{Subject: "s", Customer: "c"}, "a-one")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if _, err := store.Assign(ctx, ticket.ID, "a-two", "a-one"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if _, err := store.SetStatus(ctx, ticket.ID, desk.StatusResolved, "a-one"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := store.Ticket(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	if got.Assignee != "a-two" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "a-two")
	}
	if got.Status != desk.StatusResolved {
		t.Errorf("Status = %q, want %q", got.Status, desk.StatusResolved)
	}

	// Opened, assigned, status: the activity feed reads these, so a change
	// that records nothing is a change the dashboard never shows.
	events, err := store.Events(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if events.Total != 3 {
		t.Errorf("recorded %d events, want 3", events.Total)
	}
	if len(events.Rows) > 0 && events.Rows[0].Kind != desk.EventStatus {
		t.Errorf("newest event = %q, want %q - the feed reads newest first",
			events.Rows[0].Kind, desk.EventStatus)
	}
}

func TestTicketNotFound(t *testing.T) {
	t.Parallel()

	if _, err := desk.NewMemory().Ticket(context.Background(), "T-404"); err == nil {
		t.Fatal("Ticket on a missing id returned no error")
	}
}

func TestReadsAreCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := desk.NewMemory()
	if _, err := store.CreateTicket(ctx, desk.Ticket{Subject: "original", Customer: "c"}, "a"); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	page, err := store.Tickets(ctx, desk.Filter{})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}

	// A caller that can write into the store's own memory is a caller that
	// can race every other session reading it.
	page.Rows[0].Subject = "tampered"

	again, err := store.Tickets(ctx, desk.Filter{})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	if again.Rows[0].Subject != "original" {
		t.Errorf("writing to a returned row changed the store: subject = %q", again.Rows[0].Subject)
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := fixture(t)

	// The store is the one thing in this application that every session
	// touches, so this is the shape `-race` is here for. Component state
	// needs none of this, which is the distinction worth keeping straight.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := store.Tickets(ctx, desk.Filter{Limit: 5}); err != nil {
					t.Errorf("Tickets: %v", err)
					return
				}
				if _, err := store.CreateTicket(ctx, desk.Ticket{Subject: "s", Customer: "c"}, "a"); err != nil {
					t.Errorf("CreateTicket: %v", err)
					return
				}
				if _, err := store.Stats(ctx); err != nil {
					t.Errorf("Stats: %v", err)
					return
				}
				_ = i
			}
		}()
	}
	wg.Wait()
}

func TestStatsAgreeWithTheQueue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := fixture(t)

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	open, err := store.Tickets(ctx, desk.Filter{Status: desk.StatusOpen})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	if stats.Open != open.Total {
		t.Errorf("Stats.Open = %d but filtering for open found %d", stats.Open, open.Total)
	}
	if len(stats.Volume) != len(stats.Days) {
		t.Errorf("the chart has %d values and %d labels", len(stats.Volume), len(stats.Days))
	}
}
