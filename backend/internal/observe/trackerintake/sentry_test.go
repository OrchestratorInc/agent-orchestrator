package trackerintake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type failedSpawn struct{ err error }

func (f failedSpawn) Spawn(context.Context, ports.SpawnConfig) (domain.Session, int, int, error) {
	return domain.Session{}, 0, 0, f.err
}

type intakeTransport struct{ events chan *sentry.Event }

func (*intakeTransport) Configure(sentry.ClientOptions)        {}
func (*intakeTransport) Flush(time.Duration) bool              { return true }
func (*intakeTransport) FlushWithContext(context.Context) bool { return true }
func (*intakeTransport) Close()                                {}
func (f *intakeTransport) SendEvent(event *sentry.Event)       { f.events <- event }
func TestPollCapturesOnlyUnexpectedSpawnFaults(t *testing.T) {
	transport := &intakeTransport{events: make(chan *sentry.Event, 1)}
	if err := sentryobs.Init(sentryobs.Config{DSN: "https://public@example.com/1", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	sentryobs.SetPolicyCheck(func(context.Context) bool { return true })
	t.Cleanup(func() { sentryobs.SetPolicyCheck(nil) })
	for _, test := range []struct {
		name    string
		err     error
		capture bool
	}{
		{"user setup", apierr.Conflict("WORKSPACE_PROVISION_FAILED", "script failed", nil), false},
		{"signed out", apierr.Conflict("CHAT_AUTH_REQUIRED", "sign in", nil), false},
		{"busy", apierr.Unavailable("SERVICE_UNAVAILABLE", "busy"), false},
		{"invalid", apierr.Invalid("BAD_PROJECT", "bad project", nil), false},
		{"registered conflict", apierr.Conflict("CHAT_RESUME_FAILED", "resume failed", nil), true},
		{"registered unavailable", apierr.Unavailable("SPAWN_TIMEOUT", "timed out"), true},
		{"unclassified", errors.New("storage failed"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{projects: []domain.ProjectRecord{{ID: "demo", RepoOriginURL: "https://github.com/acme/demo.git", Config: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Enabled: true, Assignee: "*"}}}}}
			tracker := &fakeTracker{issues: []domain.Issue{{ID: domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#1"}, State: domain.IssueOpen, Assignees: []string{"alice"}}}}
			if err := New(singleResolver(tracker), store, failedSpawn{test.err}, Config{Logger: discardLogger()}).Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := sentryobs.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-transport.events:
				if !test.capture {
					t.Fatal("expected spawn state captured")
				}
			default:
				if test.capture {
					t.Fatal("unexpected fault not captured")
				}
			}
		})
	}
}
