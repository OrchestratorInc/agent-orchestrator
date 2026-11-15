package httpd

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

const terminalMuxReadLimit = 1 << 20

func mountTerminalMux(r chi.Router, mgr *terminal.Manager, log *slog.Logger, allowedOrigins []string) {
	if mgr == nil {
		return
	}
	r.Get("/mux", terminalMuxHandler(mgr, log, allowedOrigins))
}

func terminalMuxHandler(mgr *terminal.Manager, log *slog.Logger, allowedOrigins []string) http.HandlerFunc {
	allowed := exactAllowedOrigins(allowedOrigins)
	return func(w http.ResponseWriter, r *http.Request) {
		if !terminalMuxRequestAllowed(r, allowed) {
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Sec-Fetch-Site")
			envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN", "Origin is not allowed to attach terminals", nil)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			log.Warn("terminal mux: websocket upgrade failed", "err", err)
			return
		}
		c.SetReadLimit(terminalMuxReadLimit)
		mgr.Serve(r.Context(), &terminalMuxConn{c: c, fromPage: r.Header.Get("Origin") != ""})
	}
}

type terminalMuxConn struct {
	c        *websocket.Conn
	fromPage bool
}

func (a *terminalMuxConn) AllowsPanes() bool { return a.fromPage }

func (a *terminalMuxConn) ReadJSON(ctx context.Context, v any) error { return wsjson.Read(ctx, a.c, v) }
func (a *terminalMuxConn) WriteJSON(ctx context.Context, v any) error {
	return wsjson.Write(ctx, a.c, v)
}
func (a *terminalMuxConn) Ping(ctx context.Context) error { return a.c.Ping(ctx) }
func (a *terminalMuxConn) Close(reason string) error {
	return a.c.Close(websocket.StatusNormalClosure, reason)
}

func terminalMuxRequestAllowed(r *http.Request, allowed map[string]struct{}) bool {
	if _, ok := previewutil.SessionIDFromHost(r.Host); ok {
		return false
	}
	if _, ok := previewutil.SessionIDFromArtifactHost(r.Host); ok {
		return false
	}
	if _, ok := previewutil.SessionIDFromInlineArtifactHost(r.Host); ok {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return r.Header.Get("Sec-Fetch-Site") == ""
	}
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	origin := origins[0]
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "app") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(origin, " ,\t\r\n") {
		return false
	}
	if _, ok := allowed[origin]; ok {
		return true
	}
	return origin == "http://localhost" && authenticatedLANBearer(r)
}
