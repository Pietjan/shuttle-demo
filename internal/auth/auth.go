// Package auth is Deskline's stand-in for real authentication: a cookie
// naming an agent, and a middleware that resolves it before the handler
// runs.
//
// It is deliberately not a login system - there is no password anywhere in
// here - because what the quickstart needs to demonstrate is the seam, not
// the credential. Two things about that seam are real and would survive
// being swapped for OIDC tomorrow:
//
//   - The cookie is SameSite=Lax and does not carry shuttle's session id.
//     Shuttle's id travels in a header precisely so that nothing is attached
//     automatically by the browser, which is what makes CSRF not apply to an
//     action; putting it in a cookie would hand that property back.
//   - Signing out has to reach the page. A session outlives the request that
//     made it and holds the identity it captured at mount, so clearing a
//     cookie does nothing to a console already open in four tabs. That is
//     what Handler.CloseOwner is for, and SignOut below calls it.
package auth

import (
	"context"
	"net/http"

	"github.com/pietjan/shuttle-quickstart/internal/desk"
)

// CookieName is where the signed-in agent's id is kept.
const CookieName = "deskline_agent"

type ctxKey struct{}

// Middleware resolves the cookie to an agent and puts it in the request
// context. An unknown or missing cookie falls back to the first agent, so
// the quickstart is signed in the moment it starts - a real one would
// redirect to a login page here, which is the only line that would change.
func Middleware(store desk.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, ok := resolve(r, store)
		if !ok {
			http.Error(w, "no agents", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, agent)))
	})
}

// resolve reads the cookie, falling back to the first agent.
func resolve(r *http.Request, store desk.Store) (desk.Agent, bool) {
	if c, err := r.Cookie(CookieName); err == nil {
		if agent, err := store.Agent(r.Context(), c.Value); err == nil {
			return agent, true
		}
	}

	agents, err := store.Agents(r.Context())
	if err != nil || len(agents) == 0 {
		return desk.Agent{}, false
	}
	return agents[0], true
}

// From returns the signed-in agent. The zero Agent means the middleware did
// not run, which is a wiring bug rather than a state to handle.
func From(ctx context.Context) desk.Agent {
	agent, _ := ctx.Value(ctxKey{}).(desk.Agent)
	return agent
}

// SignIn switches the current agent and sends the browser back to the
// console.
//
// Closer is the shuttle handler. Switching identity has to close the old
// identity's sessions for the same reason signing out does: the console
// already open captured an agent at mount, and a new cookie does not reach
// it. Closing makes the page reconnect, find nothing, and reload - back
// through this middleware, which now answers differently.
func SignIn(store desk.Store, closer interface{ CloseOwner(string) int }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.FormValue("agent")
		agent, err := store.Agent(r.Context(), id)
		if err != nil {
			http.Error(w, "no such agent", http.StatusBadRequest)
			return
		}

		if previous := From(r.Context()); previous.ID != "" {
			closer.CloseOwner(previous.ID)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    agent.ID,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/desk/queue/", http.StatusSeeOther)
	}
}

// SignOut clears the cookie and closes every session the agent had open.
func SignOut(closer interface{ CloseOwner(string) int }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if agent := From(r.Context()); agent.ID != "" {
			closer.CloseOwner(agent.ID)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/desk/queue/", http.StatusSeeOther)
	}
}
