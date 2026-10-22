package logctx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func decodeLines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("not JSON: %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestHandlerStampsScopeFieldsOnContextLogs(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buffer, nil)))
	ctx := Begin(context.Background(), Fields{RequestID: "req-1"})
	Annotate(ctx, Fields{UserID: "user-1", OrgID: "org-1"})
	Annotate(ctx, Fields{SessionID: "session-1", Provider: "coder", ConnectionID: "conn-1"})

	logger.InfoContext(ctx, "with scope")
	// A key the call site sets explicitly wins and is not duplicated.
	logger.InfoContext(ctx, "explicit", "session_id", "explicit-session")
	// WithAttrs-bound keys are not duplicated either.
	logger.With("org_id", "bound-org").InfoContext(ctx, "bound")
	// A call without a context carries nothing extra.
	logger.Info("no context")

	lines := decodeLines(t, &buffer)
	first := lines[0]
	for key, want := range map[string]string{
		KeyRequestID: "req-1", KeyUserID: "user-1", KeyOrgID: "org-1",
		KeySessionID: "session-1", KeyProvider: "coder", KeyConnectionID: "conn-1",
	} {
		if first[key] != want {
			t.Fatalf("%s = %v, want %q (line %v)", key, first[key], want, first)
		}
	}
	if lines[1][KeySessionID] != "explicit-session" {
		t.Fatalf("explicit key overwritten: %v", lines[1])
	}
	if strings.Count(buffer.String(), `"org_id":"bound-org"`) != 1 || strings.Contains(strings.Split(buffer.String(), "\n")[2], `"org_id":"org-1"`) {
		t.Fatalf("bound key duplicated: %v", lines[2])
	}
	if _, present := lines[3][KeyRequestID]; present {
		t.Fatalf("context-free log carried scope fields: %v", lines[3])
	}
}

func TestBeginIsolatesChildScopes(t *testing.T) {
	parent := Begin(context.Background(), Fields{RequestID: "req"})
	child := Begin(parent, Fields{SessionID: "child-session"})
	Annotate(child, Fields{Provider: "freestyle"})
	if got := From(parent); got.SessionID != "" || got.Provider != "" {
		t.Fatalf("child annotations leaked into parent: %+v", got)
	}
	if got := From(child); got.RequestID != "req" || got.SessionID != "child-session" || got.Provider != "freestyle" {
		t.Fatalf("child scope = %+v", got)
	}
	// Annotate without a scope and with a nil context are no-ops.
	Annotate(context.Background(), Fields{OrgID: "x"})
	//nolint:staticcheck // a nil context must not panic
	Annotate(nil, Fields{OrgID: "x"})
	if got := From(context.Background()); got != (Fields{}) {
		t.Fatalf("unscoped From = %+v", got)
	}
}

func TestAnnotateIgnoresEmptyValues(t *testing.T) {
	ctx := Begin(context.Background(), Fields{OrgID: "org"})
	Annotate(ctx, Fields{OrgID: "", UserID: "user"})
	if got := From(ctx); got.OrgID != "org" || got.UserID != "user" {
		t.Fatalf("scope = %+v", got)
	}
	if args := (Fields{OrgID: "org"}).Args(); len(args) != 1 {
		t.Fatalf("Args = %v, want one attr", args)
	}
}
