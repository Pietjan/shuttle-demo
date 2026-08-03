package desk

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned for an id that is not in the store.
var ErrNotFound = errors.New("desk: not found")

// Filter is one query against the ticket list: what to match, how to order
// it, and which slice of the result to return.
//
// It is shaped like the query a database would run rather than like
// something to post-process in Go, because that is the point of pushing
// filtering and paging down here: the component holds one page and the store
// never hands it more.
type Filter struct {
	// Query matches the subject, the customer and the tags.
	Query string

	// Status and Assignee are exact matches, empty meaning "any". Assignee
	// takes the sentinel Unassigned to mean "nobody's".
	Status   Status
	Assignee string

	// Sort names a field; Desc reverses it.
	Sort string
	Desc bool

	Offset int
	Limit  int
}

// Unassigned is the Filter.Assignee value meaning "not assigned to anyone".
// A sentinel rather than the empty string, because empty already means "any
// assignee" and a queue needs to ask both questions.
const Unassigned = "\x00none"

// Page is one slice of a result set, with the size of the whole so a pager
// can be drawn without fetching it.
//
// This mirrors live.Page rather than being it: the domain does not import
// shuttle, so the UI layer converts. The conversion is three lines and it is
// what keeps a store swappable for one that talks to a database.
type Page[T any] struct {
	Rows  []T
	Total int
}

// Stats is the dashboard's summary, computed in one pass under one lock
// rather than as four separate queries that could disagree with each other.
type Stats struct {
	Open       int
	Pending    int
	Resolved   int
	Unassigned int
	Critical   int

	// Volume is tickets opened per day for the last 7 days, oldest first.
	Volume []float64

	// Days labels Volume.
	Days []string
}

// Store is everything the application does to its data.
//
// It is an interface so that the in-memory implementation below is visibly
// one choice rather than the only one - swapping it for Postgres means
// implementing this and changing one line in main. Every method takes a
// context for the same reason: none of them use it here, and all of them
// would.
type Store interface {
	Tickets(ctx context.Context, f Filter) (Page[Ticket], error)
	Ticket(ctx context.Context, id string) (Ticket, error)
	CreateTicket(ctx context.Context, t Ticket, actorID string) (Ticket, error)
	Assign(ctx context.Context, ticketID, agentID, actorID string) (Ticket, error)
	SetStatus(ctx context.Context, ticketID string, s Status, actorID string) (Ticket, error)
	SetPriority(ctx context.Context, ticketID string, p Priority, actorID string) (Ticket, error)

	Comments(ctx context.Context, ticketID string) ([]Comment, error)
	AddComment(ctx context.Context, c Comment) (Comment, error)

	Attachments(ctx context.Context, ticketID string) ([]Attachment, error)
	AddAttachment(ctx context.Context, a Attachment, actorID string) (Attachment, error)

	Agents(ctx context.Context) ([]Agent, error)
	Agent(ctx context.Context, id string) (Agent, error)
	SearchAgents(ctx context.Context, q string) ([]Agent, error)

	Events(ctx context.Context, offset, limit int) (Page[Event], error)
	Stats(ctx context.Context) (Stats, error)
}

// Memory is an in-memory Store, safe for concurrent use.
//
// The mutex is the whole reason this type is worth reading. A shuttle
// component needs no locks because one goroutine per session owns its
// fields; this is the opposite case - one value, every session - and the two
// live next to each other in the same program. Getting that boundary wrong
// is the bug this design exists to make hard.
//
// Every method returns values, never pointers into these slices or maps. A
// pointer handed out here is a pointer another session may be writing while
// this one renders, and `-race` would find it eventually - after somebody
// had shipped it.
type Memory struct {
	mu sync.RWMutex

	tickets     []Ticket
	comments    map[string][]Comment
	attachments map[string][]Attachment
	agents      []Agent
	events      []Event

	// now is the clock, injectable so tests are not timing-dependent.
	now func() time.Time

	// seq counts per prefix, not globally. One shared counter is the
	// obvious implementation and it is wrong in a way that only shows up
	// on screen: filing a ticket also writes an event, so the refs come
	// out T-1, T-5, T-11 - and a support desk whose ticket numbers skip
	// looks like a support desk that is losing tickets.
	seq map[string]int
}

// NewMemory returns an empty store. Use Seed for one with something in it.
func NewMemory() *Memory {
	return &Memory{
		comments:    map[string][]Comment{},
		attachments: map[string][]Attachment{},
		seq:         map[string]int{},
		now:         time.Now,
	}
}

// nextID mints an id. Callers hold the write lock.
func (m *Memory) nextID(prefix string) string {
	m.seq[prefix]++
	return fmt.Sprintf("%s-%d", prefix, m.seq[prefix])
}

// Tickets applies the filter, sorts, and returns one page with the size of
// the whole match.
func (m *Memory) Tickets(_ context.Context, f Filter) (Page[Ticket], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	matched := make([]Ticket, 0, len(m.tickets))
	for _, t := range m.tickets {
		if !matches(t, f) {
			continue
		}
		matched = append(matched, t)
	}

	sortTickets(matched, f)

	total := len(matched)
	if f.Offset >= total {
		return Page[Ticket]{Total: total}, nil
	}
	end := total
	if f.Limit > 0 {
		end = min(f.Offset+f.Limit, total)
	}

	// Copied, so the caller cannot reach the backing array this store keeps
	// writing to. matched is already a fresh slice, but its tail is shared
	// with the sub-slice being returned - and "already a copy" is exactly the
	// assumption that stops being true when somebody adds a cache here.
	page := make([]Ticket, end-f.Offset)
	copy(page, matched[f.Offset:end])
	return Page[Ticket]{Rows: page, Total: total}, nil
}

// matches is the filter's predicate, kept apart from the paging so the two
// can be read separately.
func matches(t Ticket, f Filter) bool {
	if f.Status != "" && t.Status != f.Status {
		return false
	}
	switch f.Assignee {
	case "":
	case Unassigned:
		if !t.Unassigned() {
			return false
		}
	default:
		if t.Assignee != f.Assignee {
			return false
		}
	}
	if f.Query == "" {
		return true
	}

	needle := strings.ToLower(f.Query)
	haystack := strings.ToLower(strings.Join(
		append([]string{t.ID, t.Subject, t.Customer, string(t.Status), string(t.Priority)}, t.Tags...), " "))
	return strings.Contains(haystack, needle)
}

// sortTickets orders in place. An unknown sort key falls back to the update
// time rather than returning nothing, so a stale sort in somebody's
// bookmarked URL orders the queue instead of emptying it.
func sortTickets(ts []Ticket, f Filter) {
	less := func(a, b Ticket) bool { return a.Updated.After(b.Updated) }
	switch f.Sort {
	case "subject":
		less = func(a, b Ticket) bool { return a.Subject < b.Subject }
	case "customer":
		less = func(a, b Ticket) bool { return a.Customer < b.Customer }
	case "status":
		less = func(a, b Ticket) bool { return a.Status < b.Status }
	case "priority":
		less = func(a, b Ticket) bool { return a.Priority.rank() > b.Priority.rank() }
	case "opened":
		less = func(a, b Ticket) bool { return a.Opened.After(b.Opened) }
	}

	sort.SliceStable(ts, func(i, j int) bool {
		if f.Desc {
			return less(ts[j], ts[i])
		}
		return less(ts[i], ts[j])
	})
}

// Ticket returns one ticket by id.
func (m *Memory) Ticket(_ context.Context, id string) (Ticket, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.tickets {
		if t.ID == id {
			return t, nil
		}
	}
	return Ticket{}, fmt.Errorf("%w: ticket %s", ErrNotFound, id)
}

// CreateTicket files a new ticket and records the event.
func (m *Memory) CreateTicket(_ context.Context, t Ticket, actorID string) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	t.ID = m.nextID("T")
	t.Opened, t.Updated = now, now
	if t.Status == "" {
		t.Status = StatusOpen
	}
	if t.Priority == "" {
		t.Priority = PriorityNormal
	}

	m.tickets = append(m.tickets, t)
	m.record(EventOpened, t.ID, actorID, t.Subject)
	return t, nil
}

// Assign puts a ticket on an agent, or takes it off one when agentID is
// empty.
func (m *Memory) Assign(_ context.Context, ticketID, agentID, actorID string) (Ticket, error) {
	return m.update(ticketID, actorID, EventAssigned, func(t *Ticket) string {
		t.Assignee = agentID
		if agentID == "" {
			return "unassigned"
		}
		return "assigned to " + m.nameOf(agentID)
	})
}

// SetStatus moves a ticket through its workflow.
func (m *Memory) SetStatus(_ context.Context, ticketID string, s Status, actorID string) (Ticket, error) {
	return m.update(ticketID, actorID, EventStatus, func(t *Ticket) string {
		t.Status = s
		return s.Label()
	})
}

// SetPriority changes how loudly a ticket asks for attention.
func (m *Memory) SetPriority(_ context.Context, ticketID string, p Priority, actorID string) (Ticket, error) {
	return m.update(ticketID, actorID, EventStatus, func(t *Ticket) string {
		t.Priority = p
		return p.Label() + " priority"
	})
}

// update is the shape every mutation shares: find it, change it, stamp it,
// log it. The callback returns the event's detail, because only it knows
// what it did.
func (m *Memory) update(id, actorID string, kind EventKind, fn func(*Ticket) string) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.tickets {
		if m.tickets[i].ID != id {
			continue
		}
		detail := fn(&m.tickets[i])
		m.tickets[i].Updated = m.now()
		m.record(kind, id, actorID, detail)
		return m.tickets[i], nil
	}
	return Ticket{}, fmt.Errorf("%w: ticket %s", ErrNotFound, id)
}

// nameOf resolves an agent id to a name. Callers hold the lock.
func (m *Memory) nameOf(id string) string {
	for _, a := range m.agents {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}

// record appends to the activity log. Callers hold the write lock.
func (m *Memory) record(kind EventKind, ticketID, actorID, detail string) {
	m.events = append(m.events, Event{
		ID:       m.nextID("E"),
		Kind:     kind,
		TicketID: ticketID,
		ActorID:  actorID,
		Detail:   detail,
		At:       m.now(),
	})
}

// Comments returns a ticket's thread, oldest first.
func (m *Memory) Comments(_ context.Context, ticketID string) ([]Comment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Comment, len(m.comments[ticketID]))
	copy(out, m.comments[ticketID])
	return out, nil
}

// AddComment posts to a thread and records the event.
func (m *Memory) AddComment(_ context.Context, c Comment) (Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c.ID = m.nextID("C")
	c.Posted = m.now()
	m.comments[c.TicketID] = append(m.comments[c.TicketID], c)

	detail := "replied"
	if c.Internal {
		detail = "left an internal note"
	}
	m.record(EventCommented, c.TicketID, c.AuthorID, detail)
	m.touch(c.TicketID)
	return c, nil
}

// Attachments returns a ticket's files.
func (m *Memory) Attachments(_ context.Context, ticketID string) ([]Attachment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Attachment, len(m.attachments[ticketID]))
	copy(out, m.attachments[ticketID])
	return out, nil
}

// AddAttachment records a file against a ticket. The bytes are not this
// store's business - the component saved them before calling.
func (m *Memory) AddAttachment(_ context.Context, a Attachment, actorID string) (Attachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a.ID = m.nextID("A")
	a.Added = m.now()
	m.attachments[a.TicketID] = append(m.attachments[a.TicketID], a)
	m.record(EventAttached, a.TicketID, actorID, a.Name)
	m.touch(a.TicketID)
	return a, nil
}

// touch bumps a ticket's update time. Callers hold the write lock.
func (m *Memory) touch(ticketID string) {
	for i := range m.tickets {
		if m.tickets[i].ID == ticketID {
			m.tickets[i].Updated = m.now()
			return
		}
	}
}

// Agents returns everyone, for a roster.
func (m *Memory) Agents(_ context.Context) ([]Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Agent, len(m.agents))
	copy(out, m.agents)
	return out, nil
}

// Agent resolves one id.
func (m *Memory) Agent(_ context.Context, id string) (Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, a := range m.agents {
		if a.ID == id {
			return a, nil
		}
	}
	return Agent{}, fmt.Errorf("%w: agent %s", ErrNotFound, id)
}

// SearchAgents is what the assignee combobox runs. It stays on the server,
// which is the entire reason that component needs one.
func (m *Memory) SearchAgents(_ context.Context, q string) ([]Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	needle := strings.ToLower(q)
	var out []Agent
	for _, a := range m.agents {
		hay := strings.ToLower(a.Name + " " + a.Team + " " + a.Email)
		if needle == "" || strings.Contains(hay, needle) {
			out = append(out, a)
		}
	}
	return out, nil
}

// Events returns the activity log newest first, one page at a time - which
// is what an infinite-scroll feed reads.
func (m *Memory) Events(_ context.Context, offset, limit int) (Page[Event], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := len(m.events)
	if offset >= total {
		return Page[Event]{Total: total}, nil
	}

	// Newest first, so index i counts back from the end.
	end := total
	if limit > 0 {
		end = min(offset+limit, total)
	}
	out := make([]Event, 0, end-offset)
	for i := offset; i < end; i++ {
		out = append(out, m.events[total-1-i])
	}
	return Page[Event]{Rows: out, Total: total}, nil
}

// Stats summarises the queue in one pass under one lock. Four separate
// queries could each be right and still disagree with each other, which on a
// dashboard reads as a bug in the arithmetic.
func (m *Memory) Stats(_ context.Context) (Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var s Stats
	for _, t := range m.tickets {
		switch t.Status {
		case StatusOpen:
			s.Open++
		case StatusPending:
			s.Pending++
		case StatusResolved:
			s.Resolved++
		}
		if t.Unassigned() && t.Status.Open() {
			s.Unassigned++
		}
		if t.Priority == PriorityCritical && t.Status.Open() {
			s.Critical++
		}
	}

	// Seven days of opened counts, oldest first.
	const days = 7
	today := m.now().Truncate(24 * time.Hour)
	s.Volume = make([]float64, days)
	s.Days = make([]string, days)
	for i := range days {
		day := today.AddDate(0, 0, -(days - 1 - i))
		s.Days[i] = day.Format("Mon")
		for _, t := range m.tickets {
			if t.Opened.Truncate(24 * time.Hour).Equal(day) {
				s.Volume[i]++
			}
		}
	}
	return s, nil
}
