// Package ui is Deskline's presentation layer: the shuttle components that
// hold per-page state, and the .templ views that render them.
//
// Components are plain structs. Their fields are the state of one connected
// page, they need no locks because one goroutine per session owns them, and
// their actions are closures registered per render rather than named strings
// - so a handler is type-checked, captures loop variables, and cannot be
// invoked by a client it was never rendered for.
//
// The views are .templ files. That works because shuttle renders the
// component returned by Render with the same context it handed to Render, so
// `shuttle.OnClick(ctx, …)` inside a template registers into that render
// pass exactly as it would in Go. It is worth knowing why rather than
// copying: `ctx` is in scope in every templ template, and it is the ctx that
// carries the action table.
package ui

import (
	"context"
	"strings"

	"github.com/a-h/templ"

	"github.com/pietjan/shuttle"

	"github.com/pietjan/shuttle-demo/internal/auth"
	"github.com/pietjan/shuttle-demo/internal/desk"
)

// The console's sections. Each is a path under /desk and a keyed child.
const (
	sectionQueue     = "queue"
	sectionTicket    = "ticket"
	sectionCompose   = "new"
	sectionDashboard = "dashboard"
)

// Presence is what a page publishes about itself to the roster.
//
// Deliberately not desk.Agent. A roster is delivered to every subscriber on
// the topic and then rendered into their markup, so it should carry what
// belongs on screen and nothing else - the email on desk.Agent has no
// business travelling to every other agent's browser just because the
// struct it lives on was convenient.
type Presence struct {
	ID       string
	Name     string
	Team     string
	Initials string
}

// Console is the whole application as one component, and therefore one
// session and one stream.
//
// The alternative - a handler per screen, linked with hrefs - costs a page
// load, a session and a stream per navigation, and a browser allows about
// six connections per origin. An agent who opens four tickets in tabs has
// spent the budget. Here a link is an action: Navigate pushes the path into
// history from the server and re-renders, so the address bar, the back
// button and deep links all keep working and the connection is never
// touched.
type Console struct {
	shuttle.Base
	store desk.Store

	// agent is copied at mount rather than read per action. The request
	// context that carried it is gone by the time an action runs, and async
	// work gets a background context - so a component that reaches for
	// auth.From(ctx) later finds nothing.
	agent desk.Agent

	section  string
	ticketID string

	// unassigned drives the badge on the queue link. It is a count rather
	// than a list, because the list belongs to the queue screen and holding
	// it twice is how the two start disagreeing.
	unassigned int

	notice     string
	noticeTone string
}

// NewConsole is the factory the handler mounts per page.
func NewConsole(store desk.Store) *Console {
	return &Console{store: store}
}

// Mount captures the identity and joins the room.
func (c *Console) Mount(ctx context.Context, _ shuttle.Params) error {
	c.agent = auth.From(ctx)

	// Labelling the session is what makes logging out able to reach this
	// page. A session outlives the request that made it and holds whatever
	// identity it captured here, so nothing about a cookie expiring reaches
	// it - Handler.CloseOwner does, and it matches on this label.
	c.Session().SetOwner(c.agent.ID)

	if err := c.Join(ctx, desk.TopicPresence, Presence{
		ID:       c.agent.ID,
		Name:     c.agent.Name,
		Team:     c.agent.Team,
		Initials: c.agent.Initials(),
	}); err != nil {
		return err
	}

	// The queue topic is how another agent's claim reaches this page's
	// sidebar badge. The queue screen subscribes separately for its own
	// reasons; both are cheap, and neither knows about the other.
	if err := c.Subscribe(desk.TopicQueue); err != nil {
		return err
	}
	return c.countUnassigned(ctx)
}

// HandleParams reads the section out of the path. It runs on the first
// render too, which is what makes /desk/tickets/T-3/ work as a bookmark and
// what makes the back button work without a second hook.
func (c *Console) HandleParams(_ context.Context, _ shuttle.Params) error {
	rest := strings.Trim(strings.TrimPrefix(c.Path(), "/desk"), "/")

	switch {
	case rest == "" || rest == sectionQueue:
		c.section, c.ticketID = sectionQueue, ""
	case rest == sectionDashboard:
		c.section, c.ticketID = sectionDashboard, ""
	case rest == sectionCompose:
		c.section, c.ticketID = sectionCompose, ""
	case strings.HasPrefix(rest, "tickets/"):
		c.section = sectionTicket
		c.ticketID = strings.Trim(strings.TrimPrefix(rest, "tickets/"), "/")
	default:
		c.section, c.ticketID = sectionQueue, ""
	}
	return nil
}

// HandleInfo receives what other pages publish.
//
// It re-reads from the store rather than trusting the message, which is the
// rule that keeps this safe: the payload carries ids, every subscriber's
// HandleInfo runs on its own session's goroutine, and the store is the one
// thing both of them agree about.
func (c *Console) HandleInfo(ctx context.Context, msg any) error {
	switch msg.(type) {
	case desk.TicketChanged:
		return c.countUnassigned(ctx)
	case shuttle.PresenceEvent:
		// Nothing to update - Presence() is read at render time and shuttle
		// re-renders after this returns. Named rather than defaulted so the
		// next person knows it was considered.
		return nil
	}
	return nil
}

// HandleEvent receives what this console's children emit. A child talks to
// its parent this way rather than either holding a reference to the other.
func (c *Console) HandleEvent(ctx context.Context, name string, payload any) error {
	switch name {
	case eventCreated:
		id, _ := payload.(string)
		c.notice, c.noticeTone = "Ticket "+id+" filed.", "success"
		// Navigate runs every component's HandleParams before returning, on
		// this goroutine. That is fine here - an emitted event is delivered
		// on the session's own goroutine, same as an action - and would be a
		// race from anywhere else. See Base.Do.
		return c.Navigate(ctx, "/desk/tickets/"+id+"/")
	case eventNotice:
		msg, _ := payload.(string)
		c.notice, c.noticeTone = msg, "info"
	}
	return nil
}

// countUnassigned refreshes the sidebar badge.
func (c *Console) countUnassigned(ctx context.Context) error {
	page, err := c.store.Tickets(ctx, desk.Filter{
		Assignee: desk.Unassigned,
		Status:   desk.StatusOpen,
		// No rows wanted, only the count - which is why Page carries Total
		// separately rather than leaving it to len(Rows).
		Limit: 0,
	})
	if err != nil {
		return err
	}
	c.unassigned = page.Total
	return nil
}

// Agent is the signed-in agent, for the views.
func (c *Console) Agent() desk.Agent { return c.agent }

// Section is which screen is showing, for the navigation's current-item
// styling.
func (c *Console) Section() string { return c.section }

// Roster is who else is connected, this page included.
func (c *Console) Roster() []Presence {
	members := c.Presence(desk.TopicPresence)
	out := make([]Presence, 0, len(members))
	seen := map[string]bool{}
	for _, m := range members {
		p, ok := m.Meta.(Presence)
		if !ok {
			continue
		}
		// One agent with three tabs is one person in the room. The roster
		// counts pages, which is the honest thing for it to count, and this
		// view of it counts people.
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out
}

// dismiss clears the notice.
func (c *Console) dismiss(context.Context) error {
	c.notice, c.noticeTone = "", ""
	return nil
}

// navigate is the action behind every nav link.
func (c *Console) navigate(path string) shuttle.Action {
	return func(ctx context.Context) error {
		c.notice = ""
		return c.Navigate(ctx, path)
	}
}

// screen mounts the section's component as a keyed child.
//
// The key is the identity rule and the whole of it: the same key keeps the
// same instance and its state across this component's re-renders, a
// different key mounts a fresh one, and a key that stops being rendered is
// unmounted - which tears down its subscriptions with it. Keying a ticket by
// its id is what makes moving between two tickets mount two components
// rather than reusing one with the wrong state in it.
func (c *Console) screen(ctx context.Context) templ.Component {
	switch c.section {
	case sectionDashboard:
		return shuttle.Child(ctx, sectionDashboard, func() shuttle.Component {
			return newDashboard(c.store)
		})
	case sectionCompose:
		return shuttle.Child(ctx, sectionCompose, func() shuttle.Component {
			return newCompose(c.store, c.agent)
		})
	case sectionTicket:
		id := c.ticketID
		return shuttle.Child(ctx, "ticket:"+id, func() shuttle.Component {
			return newTicket(c.store, c.agent, id)
		})
	default:
		return shuttle.Child(ctx, sectionQueue, func() shuttle.Component {
			return newQueue(c.store, c.agent)
		})
	}
}

// agents is the roster the switcher offers. Read per render rather than
// cached, because a store this small has nothing to gain from caching and a
// cached copy is one more thing that can be stale.
func (c *Console) agents() []desk.Agent {
	agents, err := c.store.Agents(context.Background())
	if err != nil {
		return nil
	}
	return agents
}

// Render hands off to the view.
func (c *Console) Render(context.Context) templ.Component {
	return console(c)
}

// RenderError is the console's error boundary. Without one, a failed render
// after the first paint leaves the page with its old markup and the only
// trace is a line in the log.
func (c *Console) RenderError(_ context.Context, err error) templ.Component {
	return renderFailure("This screen could not be rendered.", err)
}
