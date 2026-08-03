package desk

// The pub/sub contract between sessions.
//
// Nothing here knows what a broker is - these are names and payload types,
// and the UI layer publishes them through shuttle's. Keeping the contract in
// the domain package is what stops two components inventing two spellings of
// the same topic and never hearing each other.
//
// The payloads carry ids and not rows, which is the important part. A
// message is delivered to every subscriber's own goroutine, so a Ticket
// travelling in one would be a value several sessions read while another
// writes the store. Sending the id and re-reading is one map lookup and it
// cannot go stale between the publish and the render.

// TopicQueue carries changes that affect the queue as a whole: a ticket
// filed, claimed, or moved. Every console subscribes to it.
const TopicQueue = "queue"

// TopicPresence is the roster of connected agents. Joining it is what puts
// somebody in the "who else is here" list.
const TopicPresence = "presence"

// TicketTopic is the per-ticket topic, so two agents on the same ticket see
// each other's edits without every console being woken for it.
func TicketTopic(id string) string { return "ticket:" + id }

// TicketPresenceTopic is who is currently viewing one ticket.
func TicketPresenceTopic(id string) string { return TicketTopic(id) + ":presence" }

// TicketTypingTopic is ephemeral typing state for one ticket.
func TicketTypingTopic(id string) string { return TicketTopic(id) + ":typing" }

// TicketChanged says a ticket moved. Subscribers re-read it.
type TicketChanged struct {
	TicketID string

	// ActorID is who did it, so a session can tell its own change apart from
	// somebody else's - the difference between "saved" and "Ravi just took
	// this from you".
	ActorID string

	// Kind is what happened, for a component that only cares about some of
	// it.
	Kind EventKind

	// Detail is the human-readable summary, already composed by whoever made
	// the change.
	Detail string
}

// CommentPosted says a thread grew. It carries the comment's id rather than
// its body for the same reason as above, and because a component that has
// the id can decide whether it is even showing that ticket.
type CommentPosted struct {
	TicketID  string
	CommentID string
	AuthorID  string
}

// TicketTyping says whether one agent is currently drafting on a ticket.
type TicketTyping struct {
	TicketID string
	AgentID  string
	IsTyping bool
}
