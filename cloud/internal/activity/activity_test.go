package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/google/uuid"
)

func TestRedactMasksCredentials(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		secret string
	}{
		{"bearer", "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123'", "abcdefghijklmnopqrstuvwxyz0123"},
		{"worker header", "Authorization: Worker eyJhbGciOi.payload.sig-very-long-value", "payload.sig-very-long-value"},
		{"coder session token header", "Coder-Session-Token: s3cr3tS3ssionTokenValue123", "s3cr3tS3ssionTokenValue123"},
		{"key value", `token=ghs_abcdefghijklmnopqrstuvwxyz012345 next`, "ghs_abcdefghijklmnopqrstuvwxyz012345"},
		{"json pair", `{"api_key": "sk-proj-abcdefghijklmnopqrstuvwxyz"}`, "sk-proj-abcdefghijklmnopqrstuvwxyz"},
		{"url userinfo", "git clone https://x-access-token:ghs_SECRETSECRETSECRETSECRET@github.com/acme/repo", "ghs_SECRETSECRETSECRETSECRET"},
		{"github pat", "pushing with github_pat_" + "11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz", "github_pat_" + "11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz"},
		{"jwt", "id eyJhbGciOiJIUzI1NiJ9." + "eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlc2ln", "eyJzdWIiOiIxMjM0In0"},
		{"aws key", "AKIA" + "ABCDEFGHIJKLMNOP in env", "AKIA" + "ABCDEFGHIJKLMNOP"},
		{"stripe", "whsec_" + "abcdefghijklmno and sk_" + "live_abcdefghijkl", "sk_" + "live_abcdefghijkl"},
		{"pem", "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nb3BlbnNz" + "aC1rZXktdjEAAAAA\n-----END OPENSSH " + "PRIVATE KEY-----", "b3BlbnNz" + "aC1rZXktdjEAAAAA"},
		{"password", "PGPASSWORD=" + "hunter2hunter2 psql", "hunter2hunter2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("Redact(%q) = %q, still contains %q", tc.input, got, tc.secret)
			}
			if !strings.Contains(got, redactedMarker) {
				t.Fatalf("Redact(%q) = %q, want a %s marker", tc.input, got, redactedMarker)
			}
		})
	}
}

func TestRedactKeepsOrdinaryDiagnostics(t *testing.T) {
	input := "coder: worker bootstrap failed: AO worker did not start; /home/coder/.ao/worker/worker.log exited 1 (lifecycle start_error)"
	if got := Redact(input); got != input {
		t.Fatalf("Redact changed ordinary text:\n got %q\nwant %q", got, input)
	}
}

func TestExcerptKeepsTailBoundedAndClean(t *testing.T) {
	text := strings.Repeat("é line of output\n", 200) + "\x1b[31mfatal: exit 3\x1b[0m\r\n"
	got := Excerpt(text, 120)
	if len(got) > 120 {
		t.Fatalf("len = %d, want <= 120", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "fatal: exit 3") || strings.Contains(got, "\x1b") {
		t.Fatalf("excerpt = %q, want the cleaned tail", got)
	}
	if !strings.HasPrefix(got, "...") {
		t.Fatalf("excerpt = %q, want a cut marker", got)
	}
}

func TestTruncateKeepsHeadOnRuneBoundary(t *testing.T) {
	got := Truncate("code: x\n"+strings.Repeat("ü", 100), 40)
	if len(got) > 40 || !utf8.ValidString(got) || !strings.HasPrefix(got, "code: x") {
		t.Fatalf("Truncate = %q (len %d)", got, len(got))
	}
}

func TestTailLinesKeepsLastNonEmptyLines(t *testing.T) {
	got := TailLines([]string{"one", "", "two", "three", "  ", "four"}, 2, 1000)
	if got != "three\nfour" {
		t.Fatalf("TailLines = %q", got)
	}
}

func TestSanitizeBoundsAndDropsSensitiveAttrs(t *testing.T) {
	org := uuid.NewString()
	attrs := map[string]any{
		"code":          "bootstrap_failed",
		"ttr_ms":        int64(1234),
		"ok":            true,
		"duration":      1500 * time.Millisecond,
		"github_token":  "ghs_secret",
		"prompt":        "build me a thing",
		"Authorization": "Bearer x",
		"nested":        map[string]any{"a": 1},
		"long":          strings.Repeat("x", MaxAttrValueBytes*2),
		"cause":         errors.New("password=hunter2hunter2 failed"),
	}
	for index := range MaxAttrs * 2 {
		attrs["extra_"+strings.Repeat("k", 1)+string(rune('a'+index%26))+string(rune('a'+index/26))] = index
	}
	event, ok := Sanitize(domain.ActivityEvent{
		OrgID: org, UserID: "not-a-uuid", SessionID: uuid.NewString(),
		Kind: "session.start_failed", Provider: " Coder ", Attrs: attrs,
	})
	if !ok {
		t.Fatal("Sanitize rejected a valid event")
	}
	if event.UserID != "" || event.Provider != "coder" {
		t.Fatalf("user=%q provider=%q", event.UserID, event.Provider)
	}
	if len(event.Attrs) > MaxAttrs {
		t.Fatalf("%d attrs, want <= %d", len(event.Attrs), MaxAttrs)
	}
	for _, denied := range []string{"github_token", "prompt", "Authorization", "nested"} {
		if _, present := event.Attrs[denied]; present {
			t.Fatalf("attr %q was kept", denied)
		}
	}
	if long, _ := event.Attrs["long"].(string); len(long) > MaxAttrValueBytes {
		t.Fatalf("long attr is %d bytes", len(long))
	}
	if cause, _ := event.Attrs["cause"].(string); strings.Contains(cause, "hunter2") {
		t.Fatalf("cause not redacted: %q", cause)
	}
	if duration, _ := event.Attrs["duration"].(int64); duration != 1500 {
		t.Fatalf("duration = %v, want 1500 ms", event.Attrs["duration"])
	}
	encoded, err := json.Marshal(event.Attrs)
	if err != nil || len(encoded) > 8192 {
		t.Fatalf("attrs encode to %d bytes (err %v), must fit the column check", len(encoded), err)
	}

	for _, bad := range []domain.ActivityEvent{
		{OrgID: "nope", Kind: "org.created"},
		{OrgID: org, Kind: "Bad Kind"},
		{OrgID: org, Kind: ""},
	} {
		if _, ok := Sanitize(bad); ok {
			t.Fatalf("Sanitize accepted %+v", bad)
		}
	}
}

type blockingStore struct {
	release chan struct{}
	mu      sync.Mutex
	events  []domain.ActivityEvent
}

func (s *blockingStore) RecordActivityEvent(ctx context.Context, event domain.ActivityEvent) error {
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}

func TestRecordNeverBlocksOnASlowStore(t *testing.T) {
	store := &blockingStore{release: make(chan struct{})}
	recorder := NewRecorder(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{QueueSize: 4})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go recorder.Run(ctx)

	org := uuid.NewString()
	started := time.Now()
	for range 100 {
		recorder.Record(context.Background(), domain.ActivityEvent{OrgID: org, Kind: domain.ActivitySessionCreateRequested})
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("100 records took %s with a blocked store; Record must not block", elapsed)
	}
	if recorder.Dropped() == 0 {
		t.Fatal("a full queue must drop events rather than block")
	}
	close(store.release)
	deadline := time.Now().Add(5 * time.Second)
	for recorder.Written() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if recorder.Written() == 0 {
		t.Fatal("queued events were never written once the store recovered")
	}
}

type failingStore struct{}

func (failingStore) RecordActivityEvent(context.Context, domain.ActivityEvent) error {
	return errors.New("database is down")
}

func TestRecordSurvivesStoreErrorsAndNilRecorder(t *testing.T) {
	var nilRecorder *Recorder
	nilRecorder.Record(context.Background(), domain.ActivityEvent{OrgID: uuid.NewString(), Kind: "org.created"})
	nilRecorder.Log(context.Background(), domain.ActivityEvent{})

	recorder := NewRecorder(failingStore{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()
	recorder.Record(context.Background(), domain.ActivityEvent{OrgID: uuid.NewString(), Kind: "org.created"})
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestRecordLogsTheEventWithTraceFields(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(logctx.NewHandler(slog.NewJSONHandler(&buffer, nil)))
	recorder := NewRecorder(failingStore{}, logger, Options{})
	org, session := uuid.NewString(), uuid.NewString()
	ctx := logctx.Begin(context.Background(), logctx.Fields{RequestID: "req-1"})
	recorder.Record(ctx, domain.ActivityEvent{
		OrgID: org, SessionID: session, Kind: domain.ActivitySandboxReady, Provider: "freestyle",
		Attrs: map[string]any{"ttr_ms": 4200, "phase": "create"},
	})
	var line map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buffer.String())
	}
	if line["activity_kind"] != "sandbox.ready" || line["org_id"] != org || line["session_id"] != session ||
		line["sandbox_provider"] != "freestyle" || line["request_id"] != "req-1" {
		t.Fatalf("log line = %v", line)
	}
	group, _ := line["activity"].(map[string]any)
	if group["ttr_ms"] != float64(4200) {
		t.Fatalf("activity group = %v", group)
	}
	// The event's identifiers were annotated onto the request scope.
	if fields := logctx.From(ctx); fields.SessionID != session || fields.Provider != "freestyle" {
		t.Fatalf("scope = %+v", fields)
	}
}

type pruneStore struct {
	mu     sync.Mutex
	before []time.Time
}

func (s *pruneStore) PruneActivityEvents(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.before = append(s.before, before)
	return 3, nil
}

func TestRunPrunerDeletesOlderThanRetention(t *testing.T) {
	store := &pruneStore{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPruner(ctx, store, 0, time.Hour, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		count := len(store.before)
		store.mu.Unlock()
		if count > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.before) == 0 {
		t.Fatal("pruner never ran")
	}
	age := time.Since(store.before[0])
	if age < Retention-time.Minute || age > Retention+time.Minute {
		t.Fatalf("pruned before %s ago, want ~%s", age, Retention)
	}
}
