// Package logctx carries the identifiers that make a control-plane log line
// traceable (request, user, organization, session, sandbox provider and BYO
// connection) on a context, and stamps them onto every slog record logged with
// that context.
//
// A request or a reconcile claim begins one scope with Begin. Code deeper in
// the call chain learns identifiers as it goes (the authenticated user, the
// session a handler resolved) and records them with Annotate, which writes into
// that shared scope so the access-log line emitted after the handler returns
// carries them too. Only opaque identifiers belong here: never tokens, emails,
// prompts or repository names.
package logctx

import (
	"context"
	"log/slog"
	"sync"
)

// Field keys, shared with CloudWatch Logs Insights queries in
// cloud/docs/support-runbook.md. Renaming one breaks those queries.
const (
	KeyRequestID    = "request_id"
	KeyUserID       = "user_id"
	KeyOrgID        = "org_id"
	KeySessionID    = "session_id"
	KeyProvider     = "sandbox_provider"
	KeyConnectionID = "connection_id"
	KeyWorkerID     = "worker_id"
)

// Fields are the traceable identifiers known for one unit of work. Empty
// values are unknown and never logged.
type Fields struct {
	RequestID    string
	UserID       string
	OrgID        string
	SessionID    string
	Provider     string
	ConnectionID string
	WorkerID     string
}

// merge overwrites f's empty-or-stale values with every non-empty value in
// next.
func (f Fields) merge(next Fields) Fields {
	set := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	set(&f.RequestID, next.RequestID)
	set(&f.UserID, next.UserID)
	set(&f.OrgID, next.OrgID)
	set(&f.SessionID, next.SessionID)
	set(&f.Provider, next.Provider)
	set(&f.ConnectionID, next.ConnectionID)
	set(&f.WorkerID, next.WorkerID)
	return f
}

// Attrs returns the non-empty fields as slog key/value pairs in a stable order.
func (f Fields) Attrs() []slog.Attr {
	attrs := make([]slog.Attr, 0, 7)
	add := func(key, value string) {
		if value != "" {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	add(KeyRequestID, f.RequestID)
	add(KeyUserID, f.UserID)
	add(KeyOrgID, f.OrgID)
	add(KeySessionID, f.SessionID)
	add(KeyProvider, f.Provider)
	add(KeyConnectionID, f.ConnectionID)
	add(KeyWorkerID, f.WorkerID)
	return attrs
}

// Args returns the non-empty fields as alternating key/value arguments for
// slog's variadic logging methods.
func (f Fields) Args() []any {
	attrs := f.Attrs()
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	return args
}

type scope struct {
	mu     sync.Mutex
	fields Fields
}

type scopeKey struct{}

// Begin starts a new scope seeded with the parent's fields and initial. Fields
// annotated inside the returned context stay inside it.
func Begin(ctx context.Context, initial Fields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	seed := From(ctx).merge(initial)
	return context.WithValue(ctx, scopeKey{}, &scope{fields: seed})
}

// Annotate records identifiers on the nearest scope. It is a no-op when ctx
// carries no scope, so library code can call it unconditionally.
func Annotate(ctx context.Context, fields Fields) {
	if ctx == nil {
		return
	}
	current, _ := ctx.Value(scopeKey{}).(*scope)
	if current == nil {
		return
	}
	current.mu.Lock()
	current.fields = current.fields.merge(fields)
	current.mu.Unlock()
}

// From returns a snapshot of the identifiers known on ctx.
func From(ctx context.Context) Fields {
	if ctx == nil {
		return Fields{}
	}
	current, _ := ctx.Value(scopeKey{}).(*scope)
	if current == nil {
		return Fields{}
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.fields
}

// Handler stamps the context's identifiers onto every record logged through a
// Context method (InfoContext, Log, ...). A key the call site already set
// explicitly wins, so existing log lines keep their exact values and never
// carry a duplicate key.
type Handler struct {
	next slog.Handler
	// bound are top-level keys already attached with WithAttrs.
	bound map[string]bool
}

// NewHandler wraps next.
func NewHandler(next slog.Handler) *Handler {
	return &Handler{next: next}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	fields := From(ctx).Attrs()
	if len(fields) == 0 {
		return h.next.Handle(ctx, record)
	}
	present := make(map[string]bool, record.NumAttrs()+len(h.bound))
	for key := range h.bound {
		present[key] = true
	}
	record.Attrs(func(attr slog.Attr) bool {
		present[attr.Key] = true
		return true
	})
	for _, attr := range fields {
		if !present[attr.Key] {
			record.AddAttrs(attr)
		}
	}
	return h.next.Handle(ctx, record)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make(map[string]bool, len(h.bound)+len(attrs))
	for key := range h.bound {
		bound[key] = true
	}
	for _, attr := range attrs {
		bound[attr.Key] = true
	}
	return &Handler{next: h.next.WithAttrs(attrs), bound: bound}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), bound: h.bound}
}
