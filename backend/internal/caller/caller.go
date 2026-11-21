// Package caller carries the AO session whose shell issued a daemon call.
//
// The CLI sends AO_SESSION_ID in Header. The daemon only compares it with a
// session it already resolved (an action's audience), so an unknown or forged
// value matches nothing and changes nothing.
package caller

import (
	"context"
	"net/http"
)

// Header names the calling AO session on a CLI request to the daemon.
const Header = "X-AO-Caller-Session"

// ShellTerminalEnv marks a shell the user opened in a session's terminal tab.
// Those shells carry AO_SESSION_ID too, but a command typed there is not the
// agent's, so the CLI sends no caller from them.
const ShellTerminalEnv = "AO_SHELL_TERMINAL"

// maxLen bounds the header value kept on the context; session ids are short.
const maxLen = 128

type key struct{}

// With returns ctx carrying session as the caller.
func With(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, key{}, session)
}

// From returns the calling session, or "" when the request came from the
// desktop, a remote client, or outside a request.
func From(ctx context.Context) string {
	session, _ := ctx.Value(key{}).(string)
	return session
}

// Middleware puts the Header value on the request context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if session := r.Header.Get(Header); session != "" && len(session) <= maxLen {
			r = r.WithContext(With(r.Context(), session))
		}
		next.ServeHTTP(w, r)
	})
}
