package caller

import (
	"context"
	"net/http"
)

const Header = "X-AO-Caller-Session"

const ShellTerminalEnv = "AO_SHELL_TERMINAL"

const maxLen = 128

type key struct{}

func With(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, key{}, session)
}

func From(ctx context.Context) string {
	session, _ := ctx.Value(key{}).(string)
	return session
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if session := r.Header.Get(Header); session != "" && len(session) <= maxLen {
			r = r.WithContext(With(r.Context(), session))
		}
		next.ServeHTTP(w, r)
	})
}
