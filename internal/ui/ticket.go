package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/pietjan/shuttle"
	"github.com/pietjan/shuttle/live"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// streamComments is the name of the comment thread's stream. A stream is
// identified by name within a component, and the name ends up in element
// ids, so it is a constant rather than a literal in three places.
const streamComments = "comments"

// Ticket is the detail screen, and the one that puts the pieces together:
// pub/sub with another agent on the same ticket, a combobox over a set the
// browser never receives, a comment thread that is streamed rather than
// held, an upload with its own request, and a pending indicator on the one
// action slow enough to need it.
type Ticket struct {
	shuttle.Base
	store desk.Store
	agent desk.Agent

	id     string
	ticket desk.Ticket
	loaded bool

	// thread is the comments that existed when this page opened. Everything
	// posted afterwards - by this agent or another - is streamed into the
	// container and never lands here, so a ticket somebody leaves open all
	// afternoon costs what it cost when they opened it.
	//
	// The trade is the one streams always make: nothing here can rebuild
	// that list, so a reload starts from the store again. For a thread that
	// is the right trade; for an audit log it would not be.
	thread []desk.Comment

	attachments []desk.Attachment

	// picker is the assignee combobox, held rather than built in the Child
	// factory so its OnSelect can be rebuilt when this component's state
	// changes. See newTicket.
	picker *live.Combobox

	uploadErr error
	notice    string
}

func newTicket(store desk.Store, agent desk.Agent, id string) *Ticket {
	t := &Ticket{store: store, agent: agent, id: id}
	t.picker = &live.Combobox{
		Placeholder: "Assign to…",
		Label:       "Assignee",
		Empty:       "No agent by that name.",
		Search:      t.searchAgents,
		OnSelect:    t.assign,
		Limit:       8,
	}
	return t
}

// Mount loads the ticket and subscribes to it.
//
// Two topics, and the split matters: this ticket's own topic carries the
// edits only the people looking at it care about, and the queue topic
// carries the fact that it moved. Publishing everything to one topic would
// wake every console in the building for a comment on a ticket nobody else
// has open.
func (t *Ticket) Mount(ctx context.Context, _ shuttle.Params) error {
	if err := t.Subscribe(desk.TicketTopic(t.id)); err != nil {
		return err
	}
	return t.reload(ctx)
}

// reload re-reads everything this screen shows.
func (t *Ticket) reload(ctx context.Context) error {
	ticket, err := t.store.Ticket(ctx, t.id)
	if err != nil {
		if errors.Is(err, desk.ErrNotFound) {
			t.loaded = false
			return nil
		}
		return err
	}
	t.ticket, t.loaded = ticket, true

	if t.thread == nil {
		// Only on the first load. After that the thread is whatever was
		// streamed into the container, and re-reading it here would hold the
		// collection this component exists to avoid holding.
		if t.thread, err = t.store.Comments(ctx, t.id); err != nil {
			return err
		}
	}
	t.attachments, err = t.store.Attachments(ctx, t.id)
	return err
}

// Signals is this component's client-side state: what is being typed, and
// whether it is an internal note.
//
// Declaring them is what namespaces them per instance - two components
// declaring "draft" would otherwise share one value in Datastar's single
// global store - and what scopes every action's payload, so a click here
// uploads this component's signals and not the whole application's.
func (t *Ticket) Signals() map[string]any {
	return map[string]any{"draft": "", "internal": false}
}

// HandleInfo is the other half of the ticket being open in two windows.
//
// A comment is appended to the stream here rather than in the action that
// posted it, and that is not an accident: Publish reaches every subscriber
// including the page that published, so handling it in one place makes the
// local case and the remote case the same code. There is no branch for "was
// it me", and therefore no way for the two to drift apart.
func (t *Ticket) HandleInfo(ctx context.Context, msg any) error {
	switch m := msg.(type) {
	case desk.CommentPosted:
		if m.TicketID != t.id {
			return nil
		}
		comment, err := t.commentByID(ctx, m.CommentID)
		if err != nil {
			return err
		}
		return t.Stream(streamComments).Append(ctx, comment.ID, commentItem(t, comment))

	case desk.TicketChanged:
		if m.TicketID != t.id {
			return nil
		}
		if m.ActorID != t.agent.ID {
			// Somebody else moved it under this agent's cursor. Saying so is
			// the difference between a screen that changed and a screen that
			// changed for a reason.
			t.notice = m.Detail
		}
		return t.reload(ctx)
	}
	return nil
}

// commentByID re-reads a comment the message only named.
//
// A scan rather than a lookup because the thread is small and the store's
// interface is the smaller thing to keep honest; a real one would have this
// as a query.
func (t *Ticket) commentByID(ctx context.Context, id string) (desk.Comment, error) {
	comments, err := t.store.Comments(ctx, t.id)
	if err != nil {
		return desk.Comment{}, err
	}
	for _, c := range comments {
		if c.ID == id {
			return c, nil
		}
	}
	return desk.Comment{}, errors.New("ui: comment " + id + " is gone")
}

// post adds a comment.
//
// It publishes and does not append: HandleInfo does that, for this page as
// much as for anyone else's.
func (t *Ticket) post(ctx context.Context) error {
	var form struct {
		Draft    string `json:"draft"`
		Internal bool   `json:"internal"`
	}
	if err := shuttle.DecodeSignals(ctx, &form); err != nil {
		return err
	}
	if strings.TrimSpace(form.Draft) == "" {
		return nil
	}

	comment, err := t.store.AddComment(ctx, desk.Comment{
		TicketID: t.id,
		AuthorID: t.agent.ID,
		Body:     strings.TrimSpace(form.Draft),
		Internal: form.Internal,
	})
	if err != nil {
		return err
	}

	return t.Publish(ctx, desk.TicketTopic(t.id), desk.CommentPosted{
		TicketID:  t.id,
		CommentID: comment.ID,
		AuthorID:  t.agent.ID,
	})
}

// searchAgents backs the assignee combobox. It runs on the session's
// goroutine and the set never leaves the server, which is the entire reason
// this control needs one.
func (t *Ticket) searchAgents(ctx context.Context, query string) ([]live.Choice, error) {
	agents, err := t.store.SearchAgents(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]live.Choice, 0, len(agents))
	for _, a := range agents {
		out = append(out, live.Choice{
			Value:    a.ID,
			Label:    a.Name + " · " + a.Team,
			Disabled: a.ID == t.ticket.Assignee,
		})
	}
	return out, nil
}

// assign is the combobox's OnSelect.
//
// Selecting is the child's action, so it re-renders the child. This screen
// wants to show the result too, which is what Push is for - and needing to
// say so is scoped re-render working rather than an oversight.
func (t *Ticket) assign(ctx context.Context, choice live.Choice) error {
	if err := t.mutate(ctx, desk.EventAssigned, func(ctx context.Context) (desk.Ticket, string, error) {
		ticket, err := t.store.Assign(ctx, t.id, choice.Value, t.agent.ID)
		return ticket, t.agent.Name + " assigned " + t.id + " to " + choice.Label, err
	}); err != nil {
		return err
	}
	return t.Push(ctx)
}

// setStatus moves the ticket through its workflow.
func (t *Ticket) setStatus(s desk.Status) shuttle.Action {
	return func(ctx context.Context) error {
		return t.mutate(ctx, desk.EventStatus, func(ctx context.Context) (desk.Ticket, string, error) {
			ticket, err := t.store.SetStatus(ctx, t.id, s, t.agent.ID)
			return ticket, t.agent.Name + " marked " + t.id + " " + s.Label(), err
		})
	}
}

// setPriority re-triages the ticket.
func (t *Ticket) setPriority(p desk.Priority) shuttle.Action {
	return func(ctx context.Context) error {
		return t.mutate(ctx, desk.EventStatus, func(ctx context.Context) (desk.Ticket, string, error) {
			ticket, err := t.store.SetPriority(ctx, t.id, p, t.agent.ID)
			return ticket, t.agent.Name + " set " + t.id + " to " + p.Label(), err
		})
	}
}

// resolve closes the ticket, slowly and on purpose.
//
// The sleep stands in for the work a real resolve does - notifying the
// customer, closing the linked issue, writing the survey - and it is here so
// the pending indicator has something to indicate. The action POST does not
// return until this does, which is what makes the indicator cover exactly
// the work rather than approximately it.
func (t *Ticket) resolve(ctx context.Context) error {
	time.Sleep(900 * time.Millisecond)
	return t.mutate(ctx, desk.EventStatus, func(ctx context.Context) (desk.Ticket, string, error) {
		ticket, err := t.store.SetStatus(ctx, t.id, desk.StatusResolved, t.agent.ID)
		return ticket, t.agent.Name + " resolved " + t.id, err
	})
}

// mutate is the shape every change to this ticket shares: write it, tell
// this ticket's watchers, tell the queue, re-read.
//
// Both topics, every time. A change is interesting to whoever has the ticket
// open and to whoever is looking at the list it is in, and those are
// different audiences with different costs.
func (t *Ticket) mutate(
	ctx context.Context,
	kind desk.EventKind,
	apply func(context.Context) (desk.Ticket, string, error),
) error {
	ticket, detail, err := apply(ctx)
	if err != nil {
		return err
	}
	t.ticket = ticket

	changed := desk.TicketChanged{
		TicketID: ticket.ID,
		ActorID:  t.agent.ID,
		Kind:     kind,
		Detail:   detail,
	}
	if err := t.Publish(ctx, desk.TicketTopic(t.id), changed); err != nil {
		return err
	}
	return t.Publish(ctx, desk.TopicQueue, changed)
}

// Uploads declares the attachment endpoint.
//
// Every limit here is enforced again on the server, because the client's
// copy of the rules is a courtesy: the accept attribute reaches the file
// picker and any check in the browser is trivially skipped. Accept is
// checked against the file's own first bytes rather than the content type
// the client declared - that one is a string an attacker writes, and
// trusting it would let an executable through by calling itself a PNG.
func (t *Ticket) Uploads() []shuttle.Upload {
	return []shuttle.Upload{{
		Name:     "attachments",
		MaxSize:  8 << 20,
		MaxFiles: 4,
		Accept:   []string{"image/*", "application/pdf", "text/plain"},
	}}
}

// HandleUpload stores what arrived.
//
// The files are deleted when this returns, so anything worth keeping has to
// be copied out first - which Save does, cleaning the client's filename on
// the way: "../../etc/passwd" is exactly what an upload endpoint gets sent.
// Here only the metadata is kept, because the bytes are nobody's business in
// a quickstart; a real desk would Save into object storage and record the
// key.
func (t *Ticket) HandleUpload(ctx context.Context, _ string, files []*shuttle.UploadedFile) error {
	t.uploadErr = nil
	for _, f := range files {
		if _, err := t.store.AddAttachment(ctx, desk.Attachment{
			TicketID: t.id,
			Name:     f.Name,
			// The detected type, not the declared one. They differ exactly
			// when it matters.
			Type: f.Type,
			Size: f.Size,
		}, t.agent.ID); err != nil {
			t.uploadErr = err
			return err
		}
	}

	if err := t.reload(ctx); err != nil {
		return err
	}
	return t.mutate(ctx, desk.EventAttached, func(context.Context) (desk.Ticket, string, error) {
		return t.ticket, t.agent.Name + " attached a file to " + t.id, nil
	})
}

// dismiss clears the "somebody else changed this" line.
func (t *Ticket) dismiss(context.Context) error {
	t.notice = ""
	return nil
}

// assigneeName resolves the current assignee for display.
func (t *Ticket) assigneeName(ctx context.Context) string {
	if t.ticket.Unassigned() {
		return ""
	}
	agent, err := t.store.Agent(ctx, t.ticket.Assignee)
	if err != nil {
		return t.ticket.Assignee
	}
	return agent.Name
}

// authorName resolves a comment's author.
func (t *Ticket) authorName(id string) string {
	agent, err := t.store.Agent(context.Background(), id)
	if err != nil {
		return id
	}
	return agent.Name
}

// back returns to the queue.
func (t *Ticket) back(ctx context.Context) error {
	return t.Navigate(ctx, "/desk/queue/")
}

// Render hands off to the view.
func (t *Ticket) Render(context.Context) templ.Component { return ticketScreen(t) }

// RenderError keeps a failure here from costing the console around it.
func (t *Ticket) RenderError(_ context.Context, err error) templ.Component {
	return renderFailure("This ticket could not be rendered.", err)
}
