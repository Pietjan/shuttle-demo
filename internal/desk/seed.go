package desk

import (
	"context"
	"math/rand/v2"
	"time"
)

// Seed fills a store with something to look at: a handful of agents and
// enough tickets that the queue pages, sorts and filters like a real one.
//
// The times are relative to now rather than fixed, so the ages on screen are
// always plausible and the dashboard's seven-day chart always has something
// in it.
func Seed(store *Memory) {
	ctx := context.Background()
	now := store.now()

	store.mu.Lock()
	store.agents = []Agent{
		{ID: "a-nadia", Name: "Nadia Okafor", Email: "nadia@deskline.test", Team: "frontline"},
		{ID: "a-ravi", Name: "Ravi Menon", Email: "ravi@deskline.test", Team: "frontline"},
		{ID: "a-tomas", Name: "Tomás Iglesias", Email: "tomas@deskline.test", Team: "billing"},
		{ID: "a-imani", Name: "Imani Brooks", Email: "imani@deskline.test", Team: "billing"},
		{ID: "a-sofia", Name: "Sofia Lindqvist", Email: "sofia@deskline.test", Team: "escalations"},
		{ID: "a-jonas", Name: "Jonas Weber", Email: "jonas@deskline.test", Team: "escalations"},
	}
	store.mu.Unlock()

	// A fixed seed, so the queue is the same on every restart. A demo whose
	// contents shuffle is a demo nobody can describe to anybody else.
	rng := rand.New(rand.NewPCG(20240802, 7))

	for i, s := range seeds {
		// Spread over the last seven days, newest first, so the volume chart
		// has a shape and the ages on screen differ.
		age := time.Duration(i) * 5 * time.Hour
		opened := now.Add(-age)

		t, err := store.CreateTicket(ctx, Ticket{
			Subject:  s.subject,
			Body:     s.body,
			Customer: s.customer,
			Status:   s.status,
			Priority: s.priority,
			Tags:     s.tags,
		}, "system")
		if err != nil {
			// Nothing here can fail; a panic beats a silently empty queue.
			panic("desk: seeding: " + err.Error())
		}

		store.mu.Lock()
		for j := range store.tickets {
			if store.tickets[j].ID != t.ID {
				continue
			}
			store.tickets[j].Opened = opened
			store.tickets[j].Updated = opened.Add(time.Duration(rng.IntN(120)) * time.Minute)
			if s.assignee != "" {
				store.tickets[j].Assignee = s.assignee
			}
		}
		store.mu.Unlock()

		for _, c := range s.comments {
			if _, err := store.AddComment(ctx, Comment{
				TicketID: t.ID,
				AuthorID: c.author,
				Body:     c.body,
				Internal: c.internal,
			}); err != nil {
				panic("desk: seeding comments: " + err.Error())
			}
		}
	}
}

type seedComment struct {
	author   string
	body     string
	internal bool
}

type seedTicket struct {
	subject  string
	body     string
	customer string
	status   Status
	priority Priority
	assignee string
	tags     []string
	comments []seedComment
}

// seeds is the fixture set. Deliberately more than one page of them, with a
// spread of statuses and priorities and several left unassigned - the queue
// is only interesting when there is something in it to claim.
var seeds = []seedTicket{
	{
		subject: "Card declined on renewal but bank says approved", customer: "Vantage Freight",
		body:     "Our annual renewal failed three times this morning. The bank shows three approvals on their side, so something is dropping the confirmation.",
		status:   StatusOpen,
		priority: PriorityCritical,
		tags:     []string{"billing", "payments"},
		comments: []seedComment{
			{author: "a-tomas", body: "Pulled the gateway log - all three are 3DS timeouts, not declines.", internal: true},
		},
	},
	{
		subject: "SSO login loops back to the sign-in page", customer: "Northwind Health",
		body:   "Since Tuesday, staff signing in through Okta land back on the login screen. Password sign-in still works.",
		status: StatusOpen, priority: PriorityCritical, assignee: "a-sofia",
		tags: []string{"auth", "sso"},
		comments: []seedComment{
			{author: "a-sofia", body: "Reproduced. Their IdP is sending a NameID format we stopped accepting in 4.2."},
			{author: "a-jonas", body: "Do we roll back 4.2 for them or ship the compat shim?", internal: true},
		},
	},
	{
		subject: "Export to CSV truncates at 10,000 rows", customer: "Bramble & Co",
		body:   "Every export stops at exactly 10,000 rows with no warning. We only noticed after reconciling a quarter.",
		status: StatusOpen, priority: PriorityHigh,
		tags: []string{"exports"},
	},
	{
		subject: "Webhook retries hammering our endpoint", customer: "Kestrel Logistics",
		body:   "We returned 500s for about an hour and got 40,000 retries. Is there a backoff setting we are missing?",
		status: StatusPending, priority: PriorityHigh, assignee: "a-ravi",
		tags: []string{"api", "webhooks"},
		comments: []seedComment{
			{author: "a-ravi", body: "Backoff is exponential but capped at 60s. Asked engineering whether the cap is configurable."},
		},
	},
	{
		subject: "Invoice PDF shows the wrong VAT rate for Ireland", customer: "Clover Retail",
		body:   "Irish invoices are rendering at 20% rather than 23%. Our accountant flagged it.",
		status: StatusOpen, priority: PriorityHigh, assignee: "a-imani",
		tags: []string{"billing", "tax"},
	},
	{
		subject: "Can we bulk-import users from a spreadsheet?", customer: "Pinehurst Academy",
		body:   "We are onboarding 400 staff in September and would rather not add them one at a time.",
		status: StatusPending, priority: PriorityNormal, assignee: "a-nadia",
		tags: []string{"onboarding"},
		comments: []seedComment{
			{author: "a-nadia", body: "Sent the CSV template and the column reference."},
		},
	},
	{
		subject: "Two-factor codes rejected on Android only", customer: "Halcyon Media",
		body:   "iOS staff are fine. Android users get 'code expired' even seconds after it appears.",
		status: StatusOpen, priority: PriorityHigh,
		tags: []string{"auth", "mobile"},
	},
	{
		subject: "Requesting a data processing agreement", customer: "Meridian Legal",
		body:   "Procurement needs a signed DPA before we can renew. Who should we send it to?",
		status: StatusPending, priority: PriorityNormal, assignee: "a-imani",
		tags: []string{"legal", "compliance"},
	},
	{
		subject: "Search returns nothing for hyphenated names", customer: "Ashgrove Clinic",
		body:   "Searching for 'Smith-Jones' returns nothing; 'Smith' finds the record fine.",
		status: StatusOpen, priority: PriorityNormal,
		tags: []string{"search"},
	},
	{
		subject: "Dashboard loads slowly for large accounts", customer: "Vantage Freight",
		body:   "Around fifteen seconds for our main workspace. Smaller ones are instant.",
		status: StatusOpen, priority: PriorityNormal, assignee: "a-ravi",
		tags: []string{"performance"},
	},
	{
		subject: "Timezone on scheduled reports is off by one hour", customer: "Northwind Health",
		body:   "Reports scheduled for 08:00 arrive at 07:00 since the clocks changed.",
		status: StatusResolved, priority: PriorityNormal, assignee: "a-nadia",
		tags: []string{"reports", "timezones"},
		comments: []seedComment{
			{author: "a-nadia", body: "Fixed in 4.2.1 - the scheduler was storing UTC offsets rather than zone names."},
		},
	},
	{
		subject: "Add a read-only role for auditors", customer: "Meridian Legal",
		body:   "Our auditors need to see everything and change nothing. Viewer still allows comments.",
		status: StatusPending, priority: PriorityLow, assignee: "a-jonas",
		tags: []string{"permissions", "feature-request"},
	},
	{
		subject: "Attachment upload fails over 20MB", customer: "Halcyon Media",
		body:   "Anything larger than about 20MB fails silently after the progress bar completes.",
		status: StatusOpen, priority: PriorityNormal,
		tags: []string{"uploads"},
	},
	{
		subject: "Duplicate notification emails for the same comment", customer: "Kestrel Logistics",
		body:   "Every comment sends two emails to watchers. Started last week.",
		status: StatusOpen, priority: PriorityLow,
		tags: []string{"notifications"},
	},
	{
		subject: "How do I move a workspace to annual billing?", customer: "Bramble & Co",
		body:   "We are on monthly and want to switch to annual mid-term.",
		status: StatusResolved, priority: PriorityLow, assignee: "a-tomas",
		tags: []string{"billing"},
		comments: []seedComment{
			{author: "a-tomas", body: "Switched them over and credited the unused month."},
		},
	},
	{
		subject: "API rate limit headers missing on 429", customer: "Clover Retail",
		body:   "We get a 429 with no Retry-After, so our client cannot back off properly.",
		status: StatusOpen, priority: PriorityHigh,
		tags: []string{"api"},
	},
	{
		subject: "Custom domain certificate not renewing", customer: "Pinehurst Academy",
		body:   "help.pinehurst.example is showing an expired certificate this morning.",
		status: StatusOpen, priority: PriorityCritical, assignee: "a-jonas",
		tags: []string{"infrastructure", "tls"},
	},
	{
		subject: "Deleted tickets still appear in search", customer: "Ashgrove Clinic",
		body:   "Deleted items are gone from the list but still turn up in search results.",
		status: StatusPending, priority: PriorityNormal, assignee: "a-sofia",
		tags: []string{"search", "data"},
	},
	{
		subject: "Slack integration posts to the wrong channel", customer: "Vantage Freight",
		body:   "Escalations are going to #general rather than #support-escalations.",
		status: StatusOpen, priority: PriorityNormal,
		tags: []string{"integrations", "slack"},
	},
	{
		subject: "Feature request: saved queue filters", customer: "Northwind Health",
		body:   "Our team rebuilds the same three filters every morning. Could we save them?",
		status: StatusPending, priority: PriorityLow, assignee: "a-nadia",
		tags: []string{"feature-request"},
	},
	{
		subject: "Mobile app crashes opening a ticket with 200+ comments", customer: "Halcyon Media",
		body:   "Long threads crash the iOS app immediately on open.",
		status: StatusOpen, priority: PriorityHigh,
		tags: []string{"mobile", "performance"},
	},
	{
		subject: "Refund not showing on the account", customer: "Bramble & Co",
		body:   "You confirmed a refund on the 12th but the balance is unchanged.",
		status: StatusResolved, priority: PriorityNormal, assignee: "a-tomas",
		tags: []string{"billing", "refunds"},
	},
	{
		subject: "Password reset emails going to spam", customer: "Ashgrove Clinic",
		body:   "Most of our staff find reset emails in junk. SPF looks correct on our side.",
		status: StatusOpen, priority: PriorityNormal, assignee: "a-ravi",
		tags: []string{"email", "deliverability"},
	},
	{
		subject: "Audit log missing permission changes", customer: "Meridian Legal",
		body:   "Role changes are not recorded anywhere we can find, which our auditors need.",
		status: StatusOpen, priority: PriorityHigh,
		tags: []string{"compliance", "audit"},
	},
}
