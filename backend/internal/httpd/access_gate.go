package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/access"
	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
)

// ponytail: a project-limited grant cannot send a larger body (a spawn with big
// inline attachments). Read only the named field from a stream if that matters.
const maxGatedBodyBytes = 16 << 20

var grantOpenRoutes = map[string]bool{
	"GET /healthz":                             true,
	"GET /readyz":                              true,
	"POST /internal/telemetry/cli-invoked":     true,
	"POST /internal/telemetry/cli-usage-error": true,
	"GET /api/v1/access/whoami":                true,
	"GET /api/v1/access/actions":               true,
	"GET /api/v1/access/actions/{actionId}":    true,
}

var sessionToolRoutes = map[string]bool{
	"GET /api/v1/browser/status":           true,
	"POST /api/v1/browser/commands":        true,
	"GET /api/v1/computer-use/agent-tools": true,
	"POST /api/v1/computer-use/call":       true,
}

func ownSessionRoute(r *http.Request, rctx *chi.Context, pattern, sessionID string) bool {
	if sessionToolRoutes[r.Method+" "+pattern] {
		return true
	}
	target, err := url.PathUnescape(rctx.URLParam("sessionId"))
	return err == nil && target != "" && target == sessionID
}

type accessGate struct {
	registry       *access.Registry
	routes         chi.Routes
	sessionProject func(ctx context.Context, sessionID string) (string, bool)
	now            func() time.Time
}

func newAccessGate(registry *access.Registry, routes chi.Routes, sessions controllers.SessionService) *accessGate {
	return &accessGate{
		registry: registry,
		routes:   routes,
		now:      time.Now,
		sessionProject: func(ctx context.Context, sessionID string) (string, bool) {
			if sessions == nil {
				return "", false
			}
			session, err := sessions.Get(ctx, domain.SessionID(sessionID))
			if err != nil {
				return "", false
			}
			return string(session.ProjectID), true
		},
	}
}

func (g *accessGate) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := g.resolve(w, r)
		if !ok {
			return
		}
		r = r.WithContext(access.WithCaller(r.Context(), who))
		if who.Grant == nil {
			next.ServeHTTP(w, r)
			return
		}
		rctx := chi.NewRouteContext()
		if !g.routes.Match(rctx, r.Method, r.URL.Path) {
			notFoundJSON(w, r)
			return
		}
		pattern := rctx.RoutePattern()
		if grantOpenRoutes[r.Method+" "+pattern] || (who.Kind == access.CallerSession && ownSessionRoute(r, rctx, pattern, who.ID)) {
			next.ServeHTTP(w, r)
			return
		}
		action, found := access.ActionForRoute(r.Method, pattern)
		if !found {
			writeGrantRefused(w, r, &access.Refusal{Reason: access.ReasonCatalog,
				Message: fmt.Sprintf("the action catalog has no entry for %s %s; a caller with a grant can run only catalog actions", r.Method, pattern)})
			return
		}
		project, known, err := g.targetProject(r, rctx, action, who.Grant)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "GRANT_TARGET_UNREADABLE", err.Error(), nil)
			return
		}
		if refusal := who.Grant.Check(action, project, known, g.now()); refusal != nil {
			writeGrantRefused(w, r, refusal)
			return
		}
		if !action.StartsSession() {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &startedSessionRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if id := recorder.sessionID(); id != "" {
			_ = g.registry.Bind(id, who.TokenID)
		}
	})
}

func writeGrantRefused(w http.ResponseWriter, r *http.Request, refusal *access.Refusal) {
	details := map[string]any{"reason": refusal.Reason}
	if refusal.Action != "" {
		details["action"] = refusal.Action
	}
	if refusal.Group != "" {
		details["group"] = string(refusal.Group)
	}
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "GRANT_REFUSED", refusal.Message, details)
}

func tokenCaller(token access.Token) access.Caller {
	grant := token.Grant
	return access.Caller{Kind: access.CallerToken, ID: token.ID, Name: token.Name, Grant: &grant, TokenID: token.ID}
}

func (g *accessGate) resolve(w http.ResponseWriter, r *http.Request) (access.Caller, bool) {
	now := g.now()
	credential := requestscope.CredentialFrom(r.Context())
	if credential.Kind == requestscope.CredentialAgent {
		token, ok := g.registry.Get(credential.TokenID)
		if !ok || !token.Active(now) {
			writeAgentTokenInvalid(w, r)
			return access.Caller{}, false
		}
		return tokenCaller(token), true
	}
	if !requestscope.IsLAN(r.Context()) {
		if secret := bearerToken(r); strings.HasPrefix(secret, access.TokenPrefix) {
			token, ok := g.registry.Match(secret, now)
			if !ok {
				writeAgentTokenInvalid(w, r)
				return access.Caller{}, false
			}
			g.registry.Touch(token.ID, now)
			return tokenCaller(token), true
		}
	}
	who := access.Caller{Kind: access.CallerUser}
	if credential.Kind != "" {
		who = access.Caller{Kind: access.CallerDevice, ID: credential.DeviceID, Name: string(credential.Kind)}
	}
	sessionID := caller.From(r.Context())
	if sessionID == "" {
		return who, true
	}
	who = access.Caller{Kind: access.CallerSession, ID: sessionID}
	if token, bound := g.registry.ForSession(sessionID); bound {
		grant := token.Grant
		if !token.RevokedAt.IsZero() {
			grant.ExpiresAt = token.RevokedAt
		}
		who.Grant, who.TokenID, who.Name = &grant, token.ID, token.Name
	}
	return who, true
}

func writeAgentTokenInvalid(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "AGENT_TOKEN_INVALID",
		"this agent token is unknown, revoked or expired on this machine", nil)
}

func (g *accessGate) targetProject(r *http.Request, rctx *chi.Context, action access.Action, grant *access.Grant) (project string, known bool, err error) {
	if len(grant.Projects) == 0 {
		return "", false, nil
	}
	param := func() string {
		value := rctx.URLParam(action.Key)
		if unescaped, err := url.PathUnescape(value); err == nil {
			return unescaped
		}
		return value
	}
	fromSession := func(sessionID string) (string, bool, error) {
		project, _ := g.sessionProject(r.Context(), sessionID)
		return project, true, nil
	}
	switch action.Project {
	case access.ProjectParam:
		return param(), true, nil
	case access.ProjectSession:
		return fromSession(param())
	case access.ProjectQuery:
		value := r.URL.Query().Get(action.Key)
		return value, value != "", nil
	case access.ProjectBody, access.ProjectBodySession:
		value, err := bodyField(r, action.Key)
		if err != nil || value == "" {
			return "", false, err
		}
		if action.Project == access.ProjectBodySession {
			return fromSession(value)
		}
		return value, true, nil
	case access.ProjectNone, access.ProjectAll:
	}
	return "", false, nil
}

func bodyField(r *http.Request, key string) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxGatedBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("read the request body: %w", err)
	}
	if len(raw) > maxGatedBodyBytes {
		return "", fmt.Errorf("the request body is over %d MiB, the limit for a grant that is limited to projects", maxGatedBodyBytes>>20)
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", nil //nolint:nilerr // the handler reports the malformed body itself
	}
	var value string
	_ = json.Unmarshal(fields[key], &value)
	return value, nil
}

type startedSessionRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (s *startedSessionRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *startedSessionRecorder) Write(b []byte) (int, error) {
	if s.body.Len() < 1<<20 {
		s.body.Write(b)
	}
	return s.ResponseWriter.Write(b)
}

func (s *startedSessionRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *startedSessionRecorder) sessionID() string {
	if s.status >= http.StatusMultipleChoices {
		return ""
	}
	var started struct {
		Session      struct{ ID string } `json:"session"`
		Orchestrator struct{ ID string } `json:"orchestrator"`
	}
	if json.Unmarshal(s.body.Bytes(), &started) != nil {
		return ""
	}
	if started.Session.ID != "" {
		return started.Session.ID
	}
	return started.Orchestrator.ID
}
