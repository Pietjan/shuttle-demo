// Package desk is Deskline's domain: tickets, the agents who work them, and
// the store they live in.
//
// It imports neither shuttle nor loom, and that is deliberate rather than
// tidy. A shuttle component's state is per session and per connected page;
// this is the state the whole application shares, and keeping the two in
// separate packages is what stops the second quietly becoming the first.
// Everything here would be a database in a real deployment, and the Store
// interface is where that swap happens.
package desk

import (
	"fmt"
	"strings"
	"time"
)

// Status is where a ticket is in its life.
type Status string

// The statuses a ticket moves through. Open and Pending are both "not
// finished"; the difference is whose turn it is.
const (
	StatusOpen     Status = "open"
	StatusPending  Status = "pending"
	StatusResolved Status = "resolved"
)

// Statuses lists them in workflow order, for a picker.
var Statuses = []Status{StatusOpen, StatusPending, StatusResolved}

// Label is the status as a human reads it.
func (s Status) Label() string {
	switch s {
	case StatusOpen:
		return "Open"
	case StatusPending:
		return "Pending"
	case StatusResolved:
		return "Resolved"
	default:
		return string(s)
	}
}

// Open reports whether the ticket still needs somebody.
func (s Status) Open() bool { return s != StatusResolved }

// Priority is how much a ticket wants attention.
type Priority string

// The priorities, lowest first.
const (
	PriorityLow      Priority = "low"
	PriorityNormal   Priority = "normal"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

// Priorities lists them lowest first, for a picker.
var Priorities = []Priority{PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical}

// Label is the priority as a human reads it.
func (p Priority) Label() string {
	if p == "" {
		return "Normal"
	}
	return strings.ToUpper(string(p)[:1]) + string(p)[1:]
}

// rank orders priorities for sorting. Sorting on the string would put
// critical between an unrelated pair of words, which is the sort of thing
// nobody notices until a queue is triaged wrongly.
func (p Priority) rank() int {
	switch p {
	case PriorityCritical:
		return 3
	case PriorityHigh:
		return 2
	case PriorityNormal:
		return 1
	case PriorityLow:
		return 0
	default:
		return 1
	}
}

// Agent is somebody who works the queue.
type Agent struct {
	ID    string
	Name  string
	Email string
	Team  string
}

// Initials is the avatar fallback for an agent with no photo, which here is
// all of them.
func (a Agent) Initials() string {
	fields := strings.Fields(a.Name)
	switch len(fields) {
	case 0:
		return "?"
	case 1:
		return strings.ToUpper(fields[0][:1])
	default:
		return strings.ToUpper(fields[0][:1] + fields[len(fields)-1][:1])
	}
}

// Ticket is one customer's problem.
//
// Assignee is an agent id rather than an *Agent on purpose. A pointer into
// another aggregate is a pointer other sessions are free to write while this
// one renders; an id is a value, and resolving it is a lookup this component
// does when it needs the name.
type Ticket struct {
	ID       string
	Subject  string
	Body     string
	Customer string
	Status   Status
	Priority Priority
	Assignee string
	Tags     []string
	Opened   time.Time
	Updated  time.Time
}

// Unassigned reports whether the ticket is still nobody's.
func (t Ticket) Unassigned() bool { return t.Assignee == "" }

// Age is how long the ticket has been open, rounded to something a person
// would say out loud.
func (t Ticket) Age(now time.Time) string {
	d := now.Sub(t.Opened)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Comment is one message on a ticket's thread.
//
// Comments are never held by a component - they are streamed into the page
// an item at a time - so this type only ever travels one way.
type Comment struct {
	ID       string
	TicketID string
	AuthorID string
	Body     string
	Internal bool // a note between agents rather than a reply to the customer
	Posted   time.Time
}

// Attachment is a file somebody added to a ticket.
type Attachment struct {
	ID       string
	TicketID string
	Name     string
	Type     string
	Size     int64
	Added    time.Time
}

// EventKind says what happened.
type EventKind string

// The kinds of thing the activity feed reports.
const (
	EventOpened    EventKind = "opened"
	EventAssigned  EventKind = "assigned"
	EventStatus    EventKind = "status"
	EventCommented EventKind = "commented"
	EventAttached  EventKind = "attached"
)

// Event is one entry in the activity log. It is the dashboard's feed and the
// ticket's history, recorded at the point the change is made rather than
// derived afterwards.
type Event struct {
	ID       string
	Kind     EventKind
	TicketID string
	ActorID  string
	Detail   string
	At       time.Time
}
