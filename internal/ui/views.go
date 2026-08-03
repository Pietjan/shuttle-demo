package ui

import (
	"context"
	"io"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"github.com/pietjan/loom/badge"
	"github.com/pietjan/loom/button"
	"github.com/pietjan/loom/callout"
	"github.com/pietjan/loom/icon"
	"github.com/pietjan/loom/navlist"
	"github.com/pietjan/loom/picker"
	"github.com/pietjan/shuttle"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// The event names children emit up to the console. Constants rather than
// literals because an emitted event is matched by string on the far side,
// which is the one place in this design where a typo is silent.
const (
	eventCreated = "created"
	eventNotice  = "notice"
)

// statusTone maps a status onto a badge colour, so the column reads at a
// glance rather than being another word to parse.
func statusTone(s desk.Status) badge.Option {
	switch s {
	case desk.StatusOpen:
		return badge.Blue
	case desk.StatusPending:
		return badge.Amber
	case desk.StatusResolved:
		return badge.Green
	default:
		return badge.Zinc
	}
}

// priorityTone maps a priority onto a badge colour. Critical is red and
// nothing else is, which is the point of having four of them.
func priorityTone(p desk.Priority) badge.Option {
	switch p {
	case desk.PriorityCritical:
		return badge.Red
	case desk.PriorityHigh:
		return badge.Orange
	case desk.PriorityLow:
		return badge.Zinc
	default:
		return badge.Sky
	}
}

// kindIcon is the glyph for an activity entry.
func kindIcon(k desk.EventKind) icon.Name {
	switch k {
	case desk.EventOpened:
		return icon.Plus
	case desk.EventAssigned:
		return icon.UserSwitch
	case desk.EventStatus:
		return icon.ArrowsClockwise
	case desk.EventCommented:
		return icon.ChatCircle
	case desk.EventAttached:
		return icon.Paperclip
	case desk.EventEscalated:
		return icon.Warning
	default:
		return icon.Circle
	}
}

// renderFailure is what every component's RenderError shows.
//
// The error text is included because this is an internal tool and an agent
// reading "template: nil pointer" can tell somebody useful. A customer-facing
// app would show the shape and log the cause.
func renderFailure(what string, err error) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return failure(what, err).Render(ctx, w)
	})
}

// now is the clock the views read for relative times. A variable so a test
// can pin it.
var now = time.Now

// stamp formats an absolute time for a thread or a log line.
func stamp(t time.Time) string { return t.Format("2 Jan 15:04") }

// humanSize renders a byte count the way a person would say it.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt(n/(1<<10), 10) + " kB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// itoa keeps strconv out of the templates, where an import for one call
// reads as noise.
func itoa(n int) string { return strconv.Itoa(n) }

// navItemOptions builds a nav link's options: the action that navigates
// without a page load, plus aria-current when it is the current one.
//
// It lives in Go rather than in the template because a templ block cannot
// build a slice conditionally, and spreading one it was handed is the
// idiomatic way round that.
func navItemOptions(ctx context.Context, c *Console, section, path string) []navlist.Option {
	options := []navlist.Option{
		shuttle.OnEvent(ctx, navlist.Attr, "click__prevent", c.navigate(path)),
	}
	if c.Section() == section {
		// aria-current="page", which is also what navlist styles the current
		// item off.
		options = append(options, navlist.Current())
	}
	return options
}

// scopeVariant fills the queue's current scope button and outlines the rest.
func scopeVariant(current bool) button.Option {
	if current {
		return button.Primary
	}
	return button.Outline
}

// pickerSelected marks the current agent in the switcher.
func pickerSelected(selected bool) []picker.Option {
	if selected {
		return []picker.Option{picker.Selected()}
	}
	return nil
}

// calloutTone maps a notice's tone onto loom's.
func calloutTone(tone string) callout.Option {
	switch tone {
	case "success":
		return callout.Success
	case "danger":
		return callout.Danger
	default:
		return callout.Info
	}
}
