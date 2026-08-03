package ui

import (
	"context"
	"net/url"

	"github.com/a-h/templ"

	"github.com/pietjan/shuttle"
	"github.com/pietjan/shuttle/live"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// The scopes the queue can be narrowed to. They live in the URL under a key
// the table does not use - it owns q, sort, dir, page and hide, and every
// Queryer in the tree is merged into one query string by key.
const (
	scopeAll        = ""
	scopeMine       = "mine"
	scopeUnassigned = "unassigned"
)

// Queue is the ticket list: a live.Table over a store the browser never
// receives, with a scope filter above it.
//
// The table is created here rather than inside the Child factory, because
// the factory runs once at mount and anything captured in it is a mount-time
// prop. Holding the reference is what lets this component read the table's
// own view - its filter, its sort, its page - when it needs to rebuild the
// URL around it.
type Queue struct {
	shuttle.Base
	store desk.Store
	agent desk.Agent

	table *live.Table[desk.Ticket]
	scope string
}

func newQueue(store desk.Store, agent desk.Agent) *Queue {
	q := &Queue{store: store, agent: agent}
	q.table = &live.Table[desk.Ticket]{
		Columns:    q.columns(),
		Load:       q.load,
		PageSize:   8,
		Filterable: true,
		Choosable:  true,
		Empty:      "No tickets match this view.",
	}
	return q
}

// Mount subscribes to the queue topic, so a claim in somebody else's window
// reaches this list.
func (q *Queue) Mount(context.Context, shuttle.Params) error {
	return q.Subscribe(desk.TopicQueue)
}

// HandleParams reads the scope out of the URL.
//
// The table reads its own half of the same URL through its own HandleParams
// - shuttle runs every component's, not just the root's - so neither
// component has to know the other's keys.
func (q *Queue) HandleParams(_ context.Context, p shuttle.Params) error {
	q.scope = p.Get("scope")
	return nil
}

// QueryParams contributes the scope to the page's query string. The table
// contributes q, sort, dir, page and hide; shuttle merges them.
func (q *Queue) QueryParams() url.Values {
	if q.scope == scopeAll {
		return nil
	}
	return url.Values{"scope": {q.scope}}
}

// HandleInfo is another agent's change arriving.
//
// It reloads rather than patching a row, and it re-reads from the store
// rather than trusting the message. The payload carries an id precisely so
// that it cannot be stale by the time this runs: every subscriber gets this
// on its own goroutine, and the store is the only thing they all agree on.
func (q *Queue) HandleInfo(ctx context.Context, msg any) error {
	if _, ok := msg.(desk.TicketChanged); !ok {
		return nil
	}
	return q.reload(ctx)
}

// load is the table's data source: one page, never the set.
//
// live.Query and desk.Filter say almost the same thing in two vocabularies,
// and the translation is deliberate - the domain package does not import
// shuttle, so the store can be swapped for one that talks to a database
// without the UI's types following it in there.
func (q *Queue) load(ctx context.Context, query live.Query) (live.Page[desk.Ticket], error) {
	f := desk.Filter{
		Query:  query.Filter,
		Sort:   query.Sort,
		Desc:   query.Desc,
		Offset: query.Offset,
		Limit:  query.Limit,
	}
	switch q.scope {
	case scopeMine:
		f.Assignee = q.agent.ID
	case scopeUnassigned:
		f.Assignee = desk.Unassigned
	}

	page, err := q.store.Tickets(ctx, f)
	if err != nil {
		return live.Page[desk.Ticket]{}, err
	}
	return live.Page[desk.Ticket]{Rows: page.Rows, Total: page.Total}, nil
}

// reload re-applies the current URL, which is what makes the table fetch
// again.
//
// Re-applying the URL rather than reaching into the table is the point of
// putting the view there: Replace runs every component's HandleParams and
// then pushes from the root, and the table's HandleParams is what loads a
// page. A component that kept its view in a private field would need an
// exported Reload here, and a way to keep it in step with the address bar.
func (q *Queue) reload(ctx context.Context) error {
	return q.Replace(ctx, "/desk/queue/?"+q.viewParams().Encode())
}

// viewParams is the whole page's query string: the table's view plus this
// component's scope.
func (q *Queue) viewParams() url.Values {
	values := q.table.QueryParams()
	if q.scope != scopeAll {
		values.Set("scope", q.scope)
	}
	return values
}

// setScope narrows the list.
func (q *Queue) setScope(scope string) shuttle.Action {
	return func(ctx context.Context) error {
		q.scope = scope

		values := q.table.QueryParams()
		// Page 4 of one scope is rarely a page of another, and a table asked
		// for an offset past the end renders empty rather than wrong - which
		// looks like the filter matched nothing.
		values.Del("page")
		if scope != scopeAll {
			values.Set("scope", scope)
		}

		// Replace rather than Navigate: a view somebody is still adjusting
		// should not put one entry in the back button per click.
		return q.Replace(ctx, "/desk/queue/?"+values.Encode())
	}
}

// claim assigns a ticket to the signed-in agent.
//
// Note where this ends up registered. The button is built inside a
// live.Column's Cell, which takes no context - but the component it returns
// is rendered during the table's render pass, with the table's scope in ctx.
// So the action belongs to the table, and clicking re-renders the table,
// which is exactly the scope a row action wants.
func (q *Queue) claim(id string) shuttle.Action {
	return func(ctx context.Context) error {
		ticket, err := q.store.Assign(ctx, id, q.agent.ID, q.agent.ID)
		if err != nil {
			return err
		}

		// Publish before reloading, so every other console is working from
		// the same store state this one is about to render.
		if err := q.Publish(ctx, desk.TopicQueue, desk.TicketChanged{
			TicketID: ticket.ID,
			ActorID:  q.agent.ID,
			Kind:     desk.EventAssigned,
			Detail:   q.agent.Name + " claimed " + ticket.ID,
		}); err != nil {
			return err
		}
		return q.reload(ctx)
	}
}

// autoAssign assigns a ticket using the store's round-robin policy.
func (q *Queue) autoAssign(id string) shuttle.Action {
	return func(ctx context.Context) error {
		ticket, err := q.store.AutoAssign(ctx, id, q.agent.ID)
		if err != nil {
			return err
		}

		if err := q.Publish(ctx, desk.TopicQueue, desk.TicketChanged{
			TicketID: ticket.ID,
			ActorID:  q.agent.ID,
			Kind:     desk.EventAssigned,
			Detail:   q.agent.Name + " auto-assigned " + ticket.ID,
		}); err != nil {
			return err
		}
		return q.reload(ctx)
	}
}

// release takes a ticket off whoever has it.
func (q *Queue) release(id string) shuttle.Action {
	return func(ctx context.Context) error {
		ticket, err := q.store.Assign(ctx, id, "", q.agent.ID)
		if err != nil {
			return err
		}
		if err := q.Publish(ctx, desk.TopicQueue, desk.TicketChanged{
			TicketID: ticket.ID,
			ActorID:  q.agent.ID,
			Kind:     desk.EventAssigned,
			Detail:   q.agent.Name + " released " + ticket.ID,
		}); err != nil {
			return err
		}
		return q.reload(ctx)
	}
}

// open navigates to a ticket.
//
// Registered in the table's scope like claim is, and it still works, because
// Navigate re-renders from the root rather than from the component that
// called it - so the console swaps the queue for the ticket rather than
// patching a new screen into a cell.
func (q *Queue) open(id string) shuttle.Action {
	return func(ctx context.Context) error {
		return q.Navigate(ctx, "/desk/tickets/"+id+"/")
	}
}

// assigneeOf resolves a ticket's assignee for display. An id that no longer
// resolves renders as itself rather than as an error - a queue is not the
// place to find out that an agent record was deleted.
func (q *Queue) assigneeOf(ticket desk.Ticket) (desk.Agent, bool) {
	if ticket.Unassigned() {
		return desk.Agent{}, false
	}
	agent, err := q.store.Agent(context.Background(), ticket.Assignee)
	if err != nil {
		return desk.Agent{Name: ticket.Assignee}, true
	}
	return agent, true
}

// columns is the table's shape.
//
// Each Cell returns a templ component, and each of those is rendered inside
// the table's own render pass - which is what lets a cell carry a button
// with a server-side closure on it despite Cell taking no context.
func (q *Queue) columns() []live.Column[desk.Ticket] {
	return []live.Column[desk.Ticket]{
		{
			Key: "id", Title: "Ref", Width: "w-16",
			Cell: refCell,
		},
		{
			Key: "subject", Title: "Subject", Sortable: true, Width: "w-80",
			Cell: func(t desk.Ticket) templ.Component { return subjectCell(q, t) },
		},
		{
			Key: "customer", Title: "Customer", Sortable: true, Width: "w-36",
			Cell: func(t desk.Ticket) templ.Component { return live.Text(t.Customer) },
		},
		{
			// Sortable, because triaging by priority is the whole job.
			Key: "priority", Title: "Priority", Sortable: true, Width: "w-24",
			Cell: priorityCell,
		},
		{
			// Not sortable, and not for want of a key: a status column is a
			// state, and ordering it alphabetically is nobody's intent.
			Key: "status", Title: "Status", Width: "w-24",
			Cell: statusCell,
		},
		{
			Key: "assignee", Title: "Assignee", Width: "w-36",
			Cell: func(t desk.Ticket) templ.Component { return assigneeCell(q, t) },
		},
		{
			Key: "opened", Title: "Age", Sortable: true, Width: "w-16",
			Cell: func(t desk.Ticket) templ.Component { return live.Text(t.Age(now())) },
		},
		{
			Key: "actions", Title: "", Width: "w-24",
			Cell: func(t desk.Ticket) templ.Component { return actionCell(q, t) },
		},
	}
}

// Render hands off to the view. The table is a keyed child, so its actions,
// its state and its morph target are its own - sorting it does not re-render
// the scope buttons above it.
func (q *Queue) Render(context.Context) templ.Component { return queue(q) }

// RenderError keeps a failed load from costing the whole console.
func (q *Queue) RenderError(_ context.Context, err error) templ.Component {
	return renderFailure("The queue could not be loaded.", err)
}
