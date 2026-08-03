package ui

import (
	"context"

	"github.com/a-h/templ"

	"github.com/pietjan/shuttle"
	"github.com/pietjan/shuttle/live"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// Dashboard is the summary screen: a few counts, a chart, and the activity
// log as an infinite-scroll feed.
//
// The counts and the chart are ordinary state - small, cheap, recomputed
// when something changes. The feed is the opposite case and is why it is
// here: an activity log grows without limit, so live.Feed holds one page and
// streams the rest into the browser, and the gap between what the reader has
// and what this process is keeping is the whole design.
type Dashboard struct {
	shuttle.Base
	store desk.Store

	stats desk.Stats
	feed  *live.Feed[desk.Event]
}

func newDashboard(store desk.Store) *Dashboard {
	d := &Dashboard{store: store}
	d.feed = &live.Feed[desk.Event]{
		Load:     d.loadEvents,
		Item:     d.eventRow,
		PageSize: 12,
		Empty:    "Nothing has happened yet.",
		End:      "That is the whole log.",
	}
	return d
}

// Mount subscribes to the queue so the counts move when anybody works.
func (d *Dashboard) Mount(ctx context.Context, _ shuttle.Params) error {
	if err := d.Subscribe(desk.TopicQueue); err != nil {
		return err
	}
	return d.refresh(ctx)
}

// HandleInfo recomputes on somebody else's change.
//
// The counts are refreshed and the feed is not, deliberately. A feed's
// container carries data-ignore-morph, so markup written into it by a
// re-render never arrives - the only way to change what is in there is to
// Reset it, which throws away everything the reader has scrolled through.
// Doing that because a stranger closed a ticket would be hostile.
func (d *Dashboard) HandleInfo(ctx context.Context, msg any) error {
	if _, ok := msg.(desk.TicketChanged); !ok {
		return nil
	}
	return d.refresh(ctx)
}

// refresh recomputes the summary.
func (d *Dashboard) refresh(ctx context.Context) error {
	stats, err := d.store.Stats(ctx)
	if err != nil {
		return err
	}
	d.stats = stats
	return nil
}

// loadEvents is the feed's data source, and reads the same Query and Page a
// table does - so one store method could back either.
func (d *Dashboard) loadEvents(ctx context.Context, q live.Query) (live.Page[desk.Event], error) {
	page, err := d.store.Events(ctx, q.Offset, q.Limit)
	if err != nil {
		return live.Page[desk.Event]{}, err
	}
	return live.Page[desk.Event]{Rows: page.Rows, Total: page.Total}, nil
}

// eventRow renders one activity entry. It is a plain function rather than a
// method value because that is what Feed.Item takes.
func (d *Dashboard) eventRow(e desk.Event) templ.Component {
	return activityRow(d, e)
}

// actorName resolves an event's actor. "system" is the seeder, which is
// worth naming rather than leaving as an id nobody recognises.
func (d *Dashboard) actorName(id string) string {
	if id == "system" {
		return "Deskline"
	}
	agent, err := d.store.Agent(context.Background(), id)
	if err != nil {
		return id
	}
	return agent.Name
}

// Render hands off to the view.
func (d *Dashboard) Render(context.Context) templ.Component { return dashboard(d) }

// RenderError keeps a failed summary from costing the console.
func (d *Dashboard) RenderError(_ context.Context, err error) templ.Component {
	return renderFailure("The dashboard could not be rendered.", err)
}
