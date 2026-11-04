package sentryobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

func TestCaptureBackgroundScrubsAndTagsFailure(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 1)}
	if err := Init(Config{DSN: "https://public@example.com/1", InstallID: "ins_stable", Release: "2.0.0", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyCheck(nil); enabled.Store(false) })
	SetPolicyCheck(func(context.Context) bool { return true })
	const secret = "PrivateProjectOrSession_Z9"
	CaptureBackground(context.Background(), fmt.Errorf("spawn %s: %w", secret, context.DeadlineExceeded), "session_manager", "chat_spawn", map[string]string{
		"agent": "claude-code", "reason": "provision", "session": secret, "project": secret,
	})
	event := <-transport.events
	if event.Message != "Async chat spawn failed (context_deadline)" ||
		event.Tags["component"] != "session_manager" || event.Tags["operation"] != "chat_spawn" ||
		event.Tags["agent"] != "claude-code" || event.Tags["reason"] != "provision" ||
		event.Tags["error_type"] != "context_deadline" || event.Tags["session"] != "" ||
		len(event.Fingerprint) != 1 || !safeFingerprint.MatchString(event.Fingerprint[0]) {
		t.Fatalf("background capture = %+v", event)
	}
	encoded, err := event.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("background capture leaked sensitive context: %s, marshal error: %v", encoded, err)
	}
}

func TestCaptureBackgroundSkipsCanceledAndUnregistered(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 4)}
	if err := Init(Config{DSN: "https://public@example.com/1", Release: "2.0.0", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyCheck(nil); enabled.Store(false) })
	SetPolicyCheck(func(context.Context) bool { return true })
	CaptureBackground(context.Background(), nil, "reaper", "reaper_tick", nil)
	CaptureBackground(context.Background(), fmt.Errorf("stop: %w", context.Canceled), "reaper", "reaper_tick", nil)
	CaptureBackground(context.Background(), errors.New("boom"), "reaper", "not_a_registered_operation", nil)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	CaptureBackground(canceled, errors.New("boom"), "reaper", "reaper_tick", nil)
	select {
	case event := <-transport.events:
		t.Fatalf("unexpected capture: %+v", event)
	default:
	}
	// An unknown component falls back to the generic daemon component.
	CaptureBackground(context.Background(), errors.New("boom"), "made_up", "reaper_tick", map[string]string{"agent": "Bad Agent!", "reason": "free text"})
	event := <-transport.events
	if event.Tags["component"] != "daemon" || event.Tags["agent"] != "" || event.Tags["reason"] != "" {
		t.Fatalf("unregistered tags leaked: %+v", event.Tags)
	}
}

func TestCaptureBackgroundGroupsOnlyValidatedTags(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 2)}
	if err := Init(Config{DSN: "https://public@example.com/1", Release: "2.0.0", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyCheck(nil); enabled.Store(false) })
	SetPolicyCheck(func(context.Context) bool { return true })
	CaptureBackground(context.Background(), errors.New("boom"), "reaper", "reaper_tick", nil)
	first := <-transport.events
	CaptureBackground(context.Background(), errors.New("boom"), "reaper", "reaper_tick", map[string]string{
		"agent": "/private/project", "reason": "private reason", "error_code": "SPAWN_TIMEOUT",
	})
	second := <-transport.events
	if first.Fingerprint[0] != second.Fingerprint[0] || second.Tags["error_code"] != "" {
		t.Fatalf("unvalidated extras changed grouping: %v vs %v", first, second)
	}
}

type blockingBackgroundTransport struct {
	recordingTransport
	release chan struct{}
}

func (transport *blockingBackgroundTransport) SendEvent(event *sentry.Event) {
	transport.events <- event
	<-transport.release
}

func TestCaptureBackgroundDoesNotBlockAndOptOutDrainsAdmittedSends(t *testing.T) {
	transport := &blockingBackgroundTransport{
		recordingTransport: recordingTransport{events: make(chan *sentry.Event, 5)},
		release:            make(chan struct{}),
	}
	if err := Init(Config{DSN: "https://public@example.com/1", Release: "2.0.0", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		SetPolicyCheck(nil)
		close(transport.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := Drain(ctx); err != nil {
			t.Error(err)
		}
		enabled.Store(false)
	})
	SetPolicyCheck(func(context.Context) bool { return true })
	for _, operation := range []string{"model_discovery", "agent_install", "chat_turn", "chat_spawn"} {
		returned := make(chan struct{})
		go func() {
			CaptureBackground(context.Background(), errors.New("boom"), "chat", operation, nil)
			close(returned)
		}()
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("background capture blocked on transport")
		}
		select {
		case event := <-transport.events:
			if event.Tags["operation"] != operation {
				t.Fatalf("concurrent scope mixed tags: %v", event.Tags)
			}
		case <-time.After(time.Second):
			t.Fatal("admitted send did not reach transport")
		}
	}
	// Saturation drops the diagnostic without waiting or retaining a queue.
	CaptureBackground(context.Background(), errors.New("overflow"), "chat", "chat_wake", nil)
	SetPolicyCheck(nil)
	CaptureBackground(context.Background(), errors.New("opted out"), "chat", "chat_wake", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("opt-out did not wait for admitted sends: %v", err)
	}
	select {
	case event := <-transport.events:
		t.Fatalf("saturated or opted-out capture reached transport: %v", event)
	default:
	}
}

func TestFaultCodesAreTitledAndTimeoutsDetected(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"SPAWN_TIMEOUT", "CHAT_RESUME_FAILED", "GIT_INIT_FAILED", "COMPUTER_USE_DAEMON_START_FAILED"} {
		if !IsFaultCode(code) || !safeErrorCode(code) {
			t.Fatalf("fault code %s not registered", code)
		}
	}
	for _, code := range []string{"WORKSPACE_PROVISION_FAILED", "WORKSPACE_CLEANUP_FAILED", "SESSION_NOT_FOUND", "SERVICE_UNAVAILABLE"} {
		if IsFaultCode(code) {
			t.Fatalf("expected state %s registered as a fault", code)
		}
	}
	if !IsServerTimeout(http.StatusServiceUnavailable, fmt.Errorf("git: %w", context.DeadlineExceeded)) ||
		IsServerTimeout(http.StatusServiceUnavailable, errors.New("database is locked")) ||
		IsServerTimeout(http.StatusInternalServerError, context.DeadlineExceeded) {
		t.Fatal("IsServerTimeout misclassified")
	}
}

func TestErrorBudgetOverflowIsPerOperation(t *testing.T) {
	when := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	errorBudget.Lock()
	previousDay, previousGroups := errorBudget.day, errorBudget.groups
	errorBudget.day = when.YearDay() + when.Year()*366
	errorBudget.groups = make(map[string]errorBudgetGroup, maxErrorFingerprintsPerDay+2)
	for i := range maxErrorFingerprintsPerDay {
		errorBudget.groups[fmt.Sprintf("%016x", i)] = errorBudgetGroup{}
	}
	errorBudget.Unlock()
	t.Cleanup(func() {
		errorBudget.Lock()
		errorBudget.day, errorBudget.groups = previousDay, previousGroups
		errorBudget.Unlock()
	})
	// Exhaust one operation's overflow bucket; a different operation still gets through.
	for i := range maxErrorsPerFingerprintMinute {
		if !allowErrorFingerprintFor(fmt.Sprintf("%016x", maxErrorFingerprintsPerDay+i), "reaper_tick", when) {
			t.Fatalf("overflow event %d was dropped", i)
		}
	}
	if allowErrorFingerprintFor("eeeeeeeeeeeeeeee", "reaper_tick", when) {
		t.Fatal("reaper_tick overflow admitted a sixth event in the minute")
	}
	if !allowErrorFingerprintFor("dddddddddddddddd", "chat_spawn", when) {
		t.Fatal("a noisy operation starved a different operation's new failure")
	}
}
