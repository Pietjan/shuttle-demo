# Deskline

A support desk console built with [shuttle](https://github.com/pietjan/shuttle)
and [loom](https://github.com/pietjan/loom) — the demo, as an application rather
than as a feature list.

Agents work a shared ticket queue. One claims a ticket and it updates on
everybody else's screen; two open the same ticket and each sees the other's
replies. That is the whole reason to hold state on the server, so it is the
thing this app is built around rather than a demo bolted onto one.

```bash
make run
```

Then <http://localhost:8080>. No database, no configuration — the store is in
memory and seeded, and the whole thing is one `go build`.

> Open it twice. Claim something in one window and watch the other.

## What is in it

| Screen         | What it is there to show                                                                                                                                                                                                                     |
| -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Queue**      | `live.Table` over a store the browser never receives — sorted, filtered, paged on the server, with the whole view in the URL. Row actions that claim a ticket and publish it.                                                                |
| **Ticket**     | Pub/sub between two agents on the same ticket, a `live.Combobox` searching a set that stays on the server, a comment thread that is _streamed_ rather than held, uploads with their own request, and a pending indicator on the slow action. |
| **New ticket** | The change/submit split: validated as you type, committed on submit, both running the same rules. Emits to its parent, which navigates.                                                                                                      |
| **Dashboard**  | Counts that move when anybody works, a server-rendered chart, and `live.Feed` over an activity log that grows without limit.                                                                                                                 |

The whole console is **one session and one stream**. `Handler.Subtree` serves
every path under `/desk`, links call `Base.Navigate`, and the address bar keeps
working without a page load ever happening. That is not a flourish: every full
page load mints a session and opens a stream, and a browser allows about six
connections per origin, so an app whose links reload the page runs out of them.

## Running it

|                     |                                                 |
| ------------------- | ----------------------------------------------- |
| `make run`          | compiles the stylesheet and the templates first |
| `make run/tls`      | over HTTP/2 — see below                         |
| `make run/live`     | reloads on file changes                         |
| `make test`         | unit tests, under `-race`                       |
| `make audit`        | tests, vet, lint, govulncheck                   |
| `make docker/build` | a `scratch` image                               |
| `make help`         | the rest                                        |

**Use `make run/tls` if you keep several tabs open.** Datastar streams over
`fetch` rather than `EventSource`, so every open page holds one of the browser's
~6 connections per origin for as long as it is on screen. On HTTP/1.1 the sixth
live page simply never loads — no error, no console message, the request never
gets a connection to go out on. Over HTTP/2 every stream shares one connection
and the limit stops existing, and browsers only speak HTTP/2 over TLS. In a
deployment that is a proxy terminating TLS with HTTP/2 on; here it is a
throwaway certificate so the failure can be seen rather than described.

## Building on it

### It resolves shuttle and loom from next door

Neither library has a tagged release, so `go.mod` has:

```
replace github.com/pietjan/shuttle => ../shuttle
replace github.com/pietjan/loom    => ../loom
```

This only builds with both checked out beside it. When shuttle tags a version,
drop both lines, `go get` it — **and change `assets/css/input.css` at the same
time**, because it points `@source` at the same directory.

### The stylesheet is the app's job

Loom ships markup, not CSS, so the classes baked into its components only exist
in a sheet whose Tailwind build was pointed at loom's source. `make css` is two
commands:

```bash
go run github.com/pietjan/loom/cmd/css -accent indigo -o assets/css/loom.css
tailwindcss -i assets/css/input.css -o assets/static/styles.css --minify
```

`assets/css/input.css` is hand-written and worth reading before changing: it
`@import`s the generated sheet (importing, not `@source`-ing — a stylesheet read
as a source compiles to nothing) and adds two sources. The second one is the
easy one to miss:

```css
@source "../../../shuttle"; /* the live/ kit's markup */
```

Deskline uses `live.Table`, `live.Combobox` and `live.Feed`, whose markup is
Tailwind exactly as loom's is. A build that sources only loom compiles a page
whose pager has no layout and whose combobox popover has no panel — with no
error anywhere.

The compiled sheet is committed and `//go:embed`ed, which is what lets the
container image be `scratch`.

### Where things live

```
cmd/deskline/       wiring: flags, routes, the handler, TLS, shutdown
internal/desk/      the domain — imports neither shuttle nor loom
internal/auth/      a cookie standing in for real authentication
internal/ui/        components (.go) and views (.templ), one pair per screen
assets/             the stylesheet, embedded
```

Views are `.templ` files. That works because shuttle renders the component
returned by `Render` with the same context it handed to `Render`, so
`shuttle.OnClick(ctx, …)` and `shuttle.Child(ctx, …)` inside a template register
into that render pass exactly as they would in Go — `ctx` is in scope in every
templ template, and it is the one carrying the action table.

## Three things worth knowing before you copy this

**The store needs a lock and the components do not.** A component's fields are
the state of one connected page and one goroutine owns them, which is why there
are no mutexes anywhere in `internal/ui`. The store underneath is the opposite —
one value, every session — so it guards itself and returns values rather than
pointers into its own memory. Those two rules live one import apart, and mixing
them up is the bug this layout is shaped to prevent.

**Messages carry ids, and handlers re-read.** A `TicketChanged` says which
ticket moved, not what it now looks like. Every subscriber handles it on its own
session's goroutine, so a row travelling in a message would be read by several
sessions while another writes the store; an id plus a lookup cannot go stale
between the publish and the render.

**Publish reaches the publisher too.** The ticket screen posts a comment by
publishing and _not_ appending — `HandleInfo` appends, for this page as much as
for anyone else's. There is no branch for "was it me", so the local case and the
remote case cannot drift apart. Doing both is a duplicate element id, which
Datastar reports nothing about; `Assert().NoDuplicateIDs()` is what caught it
here, and it is worth calling in every component's test.

## Historical note

An earlier loom release had a race in class merging under concurrent rendering.
That issue is resolved upstream by updating loom's `tailwind-merge-go`
dependency.

If you see this race in a downstream app, update loom first and then re-run:

```bash
go test -race ./...
```

## Testing

`shuttle.Test` drives mount → action → render with no browser and no HTTP
server, and applies patches the way a browser would — so a component that
streams can be asserted on as a document:

```go
live := shuttle.Test(t, newQueue(store, agent))
live.Click("tbody button")
live.Assert().TextContains("tbody button", "Release").NoDuplicateIDs()
```

`internal/desk` is tested separately and concurrently, because the store is the
one thing every session touches.

```bash
make test
```
