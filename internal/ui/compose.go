package ui

import (
	"context"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/pietjan/shuttle"

	"github.com/pietjan/shuttle-demo/internal/desk"
)

// Compose files a new ticket. It is the change/submit split: OnChange
// validates as you type without committing anything, OnSubmit commits, and
// both run the same rules so the two cannot disagree.
type Compose struct {
	shuttle.Base
	store desk.Store
	agent desk.Agent

	// The committed values. What is being typed lives on the client as a
	// signal and only these are rendered back into value attributes - which
	// is the rule that keeps the morph from overwriting somebody mid-word.
	subject  string
	customer string
	body     string
	priority desk.Priority

	errors shuttle.Validation
}

func newCompose(store desk.Store, agent desk.Agent) *Compose {
	return &Compose{store: store, agent: agent, priority: desk.PriorityNormal}
}

// Signals is the form's client-side state. Keystrokes stay here until the
// debounce expires, so typing costs nothing.
func (c *Compose) Signals() map[string]any {
	return map[string]any{"subject": "", "customer": "", "body": ""}
}

// form is what the client sends. The struct is the allow-list: anything it
// does not declare is ignored, so the client can only reach fields that were
// asked for.
type composeForm struct {
	Subject  string `json:"subject"`
	Customer string `json:"customer"`
	Body     string `json:"body"`
}

// validate runs the rules and nothing else. It is bound to OnChange, which
// listens on input rather than change - change waits for blur, which is too
// late to be useful - so it fires per keystroke and its debounce is doing
// real work.
func (c *Compose) validate(ctx context.Context) error {
	form, err := c.read(ctx)
	if err != nil {
		return err
	}
	c.check(form)
	return nil
}

// check is the rules themselves, shared by validate and submit so that
// passing the first cannot mean anything different from passing the second.
func (c *Compose) check(form composeForm) {
	c.errors = shuttle.Validate()
	c.errors.Require("subject", strings.TrimSpace(form.Subject), "A subject is required.")
	c.errors.Require("customer", strings.TrimSpace(form.Customer), "Which customer is this for?")

	if s := strings.TrimSpace(form.Subject); s != "" && len(s) < 8 {
		c.errors.Add("subject", "A subject that short will not mean anything to the next agent.")
	}
	if b := strings.TrimSpace(form.Body); b != "" && len(b) < 20 {
		c.errors.Add("body", "Give the next agent something to work with.")
	}
}

// read decodes the signals into the form.
func (c *Compose) read(ctx context.Context) (composeForm, error) {
	var form composeForm
	if err := shuttle.DecodeSignals(ctx, &form); err != nil {
		return composeForm{}, err
	}
	return form, nil
}

// setPriority is the one field that is not typed, so it commits
// immediately.
func (c *Compose) setPriority(p desk.Priority) shuttle.Action {
	return func(context.Context) error {
		c.priority = p
		return nil
	}
}

// submit commits, but only if the same rules pass.
//
// The pause is not decoration: filing a ticket is the kind of work that
// touches a database and a notifier, and the indicator on the button covers
// exactly as long as this takes because the action POST does not return
// until it does.
func (c *Compose) submit(ctx context.Context) error {
	form, err := c.read(ctx)
	if err != nil {
		return err
	}

	c.check(form)
	if !c.errors.OK() {
		return nil
	}

	c.subject = strings.TrimSpace(form.Subject)
	c.customer = strings.TrimSpace(form.Customer)
	c.body = strings.TrimSpace(form.Body)

	time.Sleep(400 * time.Millisecond)

	ticket, err := c.store.CreateTicket(ctx, desk.Ticket{
		Subject:  c.subject,
		Customer: c.customer,
		Body:     c.body,
		Priority: c.priority,
		Status:   desk.StatusOpen,
	}, c.agent.ID)
	if err != nil {
		return err
	}

	// Every console's sidebar counts unassigned tickets, and this just made
	// one. Telling them is the difference between a number that is right and
	// a number that is right until somebody reloads.
	if err := c.Publish(ctx, desk.TopicQueue, desk.TicketChanged{
		TicketID: ticket.ID,
		ActorID:  c.agent.ID,
		Kind:     desk.EventOpened,
		Detail:   c.agent.Name + " filed " + ticket.ID,
	}); err != nil {
		return err
	}

	// Emit reaches the nearest ancestor implementing Receiver - the console -
	// which navigates to the new ticket. A child that navigated itself would
	// work too; emitting keeps the decision about what happens next with the
	// component that owns the screen.
	return c.Emit(ctx, eventCreated, ticket.ID)
}

// Render hands off to the view.
func (c *Compose) Render(context.Context) templ.Component { return compose(c) }

// RenderError keeps a broken form from taking the console with it.
func (c *Compose) RenderError(_ context.Context, err error) templ.Component {
	return renderFailure("The form could not be rendered.", err)
}
