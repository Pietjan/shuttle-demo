package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/pietjan/shuttle"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// These tests intentionally do not call t.Parallel.
//
// Loom previously had a class-merge race under concurrent rendering, which is
// now resolved upstream, but keeping this package sequential still improves
// failure readability and avoids interleaving action traces across component
// tests.

// agent is who the tests are signed in as.
var agent = desk.Agent{ID: "a-nadia", Name: "Nadia Okafor", Email: "n@x.test", Team: "frontline"}

// other is somebody else, for the two-windows cases.
var other = desk.Agent{ID: "a-ravi", Name: "Ravi Menon", Email: "r@x.test", Team: "frontline"}

// storeWith returns a store holding exactly the given tickets, so a test
// asserting on "the first row" knows which row that is. Seeded fixtures are
// for looking at; tests build their own.
func storeWith(t *testing.T, tickets ...desk.Ticket) *desk.Memory {
	t.Helper()

	store := desk.NewMemory()
	ctx := context.Background()
	for _, ticket := range tickets {
		if _, err := store.CreateTicket(ctx, ticket, "system"); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	return store
}

func TestQueueClaimAssignsAndPublishes(t *testing.T) {
	store := storeWith(t, desk.Ticket{
		Subject: "Card declined on renewal", Customer: "Vantage", Priority: desk.PriorityHigh,
	})

	q := newQueue(store, agent)
	live := shuttle.Test(t, q)

	live.Assert().
		TextContains("tbody", "Card declined on renewal").
		NoDuplicateIDs()

	// The claim button is built inside a live.Column's Cell, which takes no
	// context - so this asserts the thing that makes that work: the closure
	// really was registered, against the table's own action table.
	//
	// Selected by position rather than by id, because a per-row id would
	// have to be unique per row and a duplicate id degrades morphing
	// silently. NoDuplicateIDs above is what checks that.
	live.Click("tbody button")

	ticket, err := store.Ticket(context.Background(), "T-1")
	if err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	if ticket.Assignee != agent.ID {
		t.Errorf("Assignee = %q, want %q", ticket.Assignee, agent.ID)
	}

	// Claimed, so the row now offers Release rather than Claim.
	live.Assert().TextContains("tbody button", "Release").NoDuplicateIDs()
}

func TestQueueScopeLivesInTheURL(t *testing.T) {
	store := storeWith(t,
		desk.Ticket{Subject: "Unclaimed and waiting", Customer: "A"},
		desk.Ticket{Subject: "Already taken", Customer: "B"},
	)
	if _, err := store.Assign(context.Background(), "T-2", other.ID, other.ID); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	q := newQueue(store, agent)
	live := shuttle.Test(t, q)
	if !strings.Contains(live.HTML(), "Already taken") {
		t.Fatal("the unscoped queue is not showing every ticket")
	}

	// Arriving at the URL directly is what a shared link does, and it must
	// reach the same view as clicking the button would.
	live.Params("?scope=unassigned")
	live.Assert().
		TextContains("tbody", "Unclaimed and waiting").
		NoDuplicateIDs()
	if strings.Contains(live.HTML(), "Already taken") {
		t.Error("the unassigned scope is showing an assigned ticket")
	}
}

func TestQueueHearsAnotherAgentsClaim(t *testing.T) {
	store := storeWith(t, desk.Ticket{Subject: "Contested ticket", Customer: "A"})

	q := newQueue(store, agent)
	live := shuttle.Test(t, q)
	live.Assert().TextContains("tbody button", "Claim")

	// Somebody else takes it. Their write lands in the store and their
	// message arrives here, and this page re-reads rather than trusting the
	// payload - which is what makes the two agree.
	if _, err := store.Assign(context.Background(), "T-1", other.ID, other.ID); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	live.Publish(desk.TopicQueue, desk.TicketChanged{
		TicketID: "T-1", ActorID: other.ID, Kind: desk.EventAssigned, Detail: "Ravi claimed T-1",
	})

	if strings.Contains(live.HTML(), "Claim</button>") {
		t.Error("the row still offers Claim after somebody else took it")
	}
	live.Assert().NoDuplicateIDs()
}

func TestComposeValidatesWithoutCommitting(t *testing.T) {
	store := desk.NewMemory()
	live := shuttle.Test(t, newCompose(store, agent))

	live.Assert().NoDuplicateIDs()

	// Too short to mean anything to the next agent. OnChange validates
	// without committing, so nothing reaches the store.
	live.Signal("subject", "help").Signal("customer", "Vantage").Change("input")
	live.Assert().TextContains("[data-ui=\"field\"]", "will not mean anything")

	page, err := store.Tickets(context.Background(), desk.Filter{})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("validating filed %d tickets; it must not commit anything", page.Total)
	}

	// Submitting the same bad input must fail the same way, or the two rule
	// sets have drifted apart.
	live.Click("#shuttle-c-file")
	page, err = store.Tickets(context.Background(), desk.Filter{})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	if page.Total != 0 {
		t.Fatal("submit committed a ticket that validation had rejected")
	}
}

// TestComposeFilesThroughTheConsole drives the form where it actually lives.
//
// Compose ends by emitting to its parent, so mounting it alone would be
// testing a component in a tree it is never in: Emit reaches the nearest
// ancestor implementing Receiver, and without one shuttle correctly reports
// that nothing would have handled it. Mounting the console is both the
// honest test and the one that covers the navigation afterwards.
func TestComposeFilesThroughTheConsole(t *testing.T) {
	store := desk.NewMemory()
	live := shuttle.Test(t, NewConsole(store))

	live.Params("/desk/new/")
	live.Assert().
		TextContains("h1", "New ticket").
		NoDuplicateIDs()

	// The compose screen is a child, so its ids are namespaced to it - which
	// is the point of deriving them rather than writing them down.
	live.Signal("subject", "Card declined on renewal").
		Signal("customer", "Vantage Freight").
		Signal("body", "Three attempts this morning, all declined at the gateway.").
		Click("button[data-indicator]")

	page, err := store.Tickets(context.Background(), desk.Filter{})
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("filed %d tickets, want 1", page.Total)
	}
	if got := page.Rows[0].Subject; got != "Card declined on renewal" {
		t.Errorf("subject = %q", got)
	}

	// Emitted, handled, navigated: the console swapped the form for the
	// ticket it just created.
	c, ok := live.Component().(*Console)
	if !ok {
		t.Fatal("Component() is not a *Console")
	}
	if c.Section() != sectionTicket || c.ticketID != page.Rows[0].ID {
		t.Errorf("console is on %q/%q, want the new ticket %q",
			c.Section(), c.ticketID, page.Rows[0].ID)
	}
	live.Assert().NoDuplicateIDs()
}

func TestTicketStreamsComments(t *testing.T) {
	store := storeWith(t, desk.Ticket{Subject: "SSO login loops", Customer: "Northwind"})

	tk := newTicket(store, agent, "T-1")
	live := shuttle.Test(t, tk)

	live.Assert().
		TextContains("h1", "SSO login loops").
		NoDuplicateIDs()

	live.Signal("draft", "Reproduced on their staging tenant.").Click("#shuttle-c-post")

	comments, err := store.Comments(context.Background(), "T-1")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("stored %d comments, want 1", len(comments))
	}

	// The action published and did not append; HandleInfo appended, because
	// Publish reaches every subscriber including the page that published.
	// So the comment is on screen without this test delivering anything -
	// and publishing it again here would append it twice, which is what
	// NoDuplicateIDs is checking.
	//
	// The kit applies patches the way a browser would, so a streamed item
	// can be asserted on as part of the document rather than as a patch.
	live.Assert().
		TextContains("[data-ui=\"timeline\"]", "Reproduced on their staging tenant.").
		Count("[data-ui=\"timeline-item\"]", 1).
		NoDuplicateIDs()
}

func TestTicketHearsAnotherAgent(t *testing.T) {
	store := storeWith(t, desk.Ticket{Subject: "Contested", Customer: "A"})

	tk := newTicket(store, agent, "T-1")
	live := shuttle.Test(t, tk)

	if _, err := store.SetStatus(context.Background(), "T-1", desk.StatusResolved, other.ID); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	live.Publish(desk.TicketTopic("T-1"), desk.TicketChanged{
		TicketID: "T-1", ActorID: other.ID, Kind: desk.EventStatus,
		Detail: "Ravi Menon resolved T-1",
	})

	// Re-read from the store, and said so - a screen that changes under
	// somebody's cursor should say why.
	live.Assert().
		TextContains("[data-ui=\"callout\"]", "Ravi Menon resolved T-1").
		NoDuplicateIDs()
	if !strings.Contains(live.HTML(), "Resolved") {
		t.Error("the status badge did not follow the store")
	}
}

func TestTicketIgnoresOtherTicketsMessages(t *testing.T) {
	store := storeWith(t,
		desk.Ticket{Subject: "Mine", Customer: "A"},
		desk.Ticket{Subject: "Somebody else's", Customer: "B"},
	)

	tk := newTicket(store, agent, "T-1")
	live := shuttle.Test(t, tk)

	// The queue topic is shared, so a component that did not check would
	// redraw itself for every ticket in the building.
	live.Publish(desk.TicketTopic("T-1"), desk.TicketChanged{
		TicketID: "T-2", ActorID: other.ID, Detail: "should not appear",
	})
	if strings.Contains(live.HTML(), "should not appear") {
		t.Error("a message about another ticket reached this one")
	}
}

func TestTicketMissingRendersAnAnswer(t *testing.T) {
	live := shuttle.Test(t, newTicket(desk.NewMemory(), agent, "T-404"))
	live.Assert().
		TextContains("[data-ui=\"callout\"]", "No ticket T-404").
		NoDuplicateIDs()
}

func TestDashboardCountsFollowTheStore(t *testing.T) {
	store := storeWith(t,
		desk.Ticket{Subject: "One", Customer: "A", Priority: desk.PriorityCritical},
		desk.Ticket{Subject: "Two", Customer: "B"},
	)

	live := shuttle.Test(t, newDashboard(store))
	live.Assert().
		TextContains("body", "Dashboard").
		NoDuplicateIDs()

	if _, err := store.SetStatus(context.Background(), "T-1", desk.StatusResolved, other.ID); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	live.Publish(desk.TopicQueue, desk.TicketChanged{TicketID: "T-1", ActorID: other.ID})

	d, ok := live.Component().(*Dashboard)
	if !ok {
		t.Fatal("Component() is not a *Dashboard")
	}
	if d.stats.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", d.stats.Resolved)
	}
	if d.stats.Critical != 0 {
		t.Errorf("Critical = %d, want 0 once the critical one is resolved", d.stats.Critical)
	}
}

func TestConsoleRoutesOnThePath(t *testing.T) {
	store := storeWith(t, desk.Ticket{Subject: "Routable", Customer: "A"})
	live := shuttle.Test(t, NewConsole(store))

	// No path means the queue, which is what /desk/ has to land on.
	live.Assert().
		TextContains("h1", "Queue").
		NoDuplicateIDs()

	c, ok := live.Component().(*Console)
	if !ok {
		t.Fatal("Component() is not a *Console")
	}
	if c.Section() != sectionQueue {
		t.Errorf("Section() = %q, want %q", c.Section(), sectionQueue)
	}
}

func TestConsoleNavigatesToANewTicket(t *testing.T) {
	store := desk.NewMemory()
	live := shuttle.Test(t, NewConsole(store))

	c, ok := live.Component().(*Console)
	if !ok {
		t.Fatal("Component() is not a *Console")
	}

	// What Compose emits when it has filed one. The console owns what
	// happens next, which is why the child emits rather than navigating.
	if err := c.HandleEvent(context.Background(), eventCreated, "T-9"); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if c.Section() != sectionTicket {
		t.Errorf("Section() = %q, want %q", c.Section(), sectionTicket)
	}
	if c.ticketID != "T-9" {
		t.Errorf("ticketID = %q, want T-9", c.ticketID)
	}
}
