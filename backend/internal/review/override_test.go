package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	reviewerclaudecode "github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTriggerRejectsAnUnknownHarnessOverride(t *testing.T) {
	eng := New(Deps{})

	_, err := trigger(context.Background(), eng, "mer-1", "not-a-reviewer", domain.AgentConfig{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestSecondClaudeReviewRoundDoesNotReuseExistingConversation covers #6642: a
// second claude-code review round that relaunches the reviewer fresh (a
// --model/--effort/--agent config override) must not pin the first round's
// conversation id with --session-id, which Claude rejects as already in use. A
// plain second round keeps resuming the first round's conversation.
func TestSecondClaudeReviewRoundDoesNotReuseExistingConversation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config domain.AgentConfig
		resume bool
	}{
		{name: "config override", config: domain.AgentConfig{Model: "claude-review-other"}},
		{name: "plain", resume: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			configDir := stubClaudeReviewer(t)
			st := newSQLiteReviewStore(t)
			worker := liveWorker()
			worker.Metadata.WorkspacePath = t.TempDir()
			seedReviewWorker(t, st, worker)
			rt := &fakeRuntime{}
			launcher := NewLauncher(singleReviewerResolver{reviewer: reviewerclaudecode.New()}, rt, t.TempDir(),
				WithExecutable(func() (string, error) { return "/usr/local/bin/ao", nil }))
			eng := newEngineForTest(st, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)

			first, err := trigger(ctx, eng, worker.ID, "", domain.AgentConfig{})
			if err != nil || !first.Created {
				t.Fatalf("first round = (%+v, %v), want a created run", first, err)
			}
			firstID := argAfter(rt.createCfg.Argv, "--session-id")
			if firstID != workeragent.SessionUUID(reviewerHandleID(worker.ID)) {
				t.Fatalf("first round --session-id = %q, want the reviewer's deterministic id (argv %#v)", firstID, rt.createCfg.Argv)
			}
			// Claude has persisted the first round's conversation and the pass ended.
			writeClaudeTranscript(t, configDir, firstID)
			if _, err := st.UpdateReviewRunResult(ctx, first.Run.ID, domain.ReviewRunComplete, domain.VerdictChangesRequested, "fix it", "", true); err != nil {
				t.Fatalf("complete first run: %v", err)
			}

			eng.prs = prAt("sha2")
			second, err := trigger(ctx, eng, worker.ID, "", tc.config)
			if err != nil || !second.Created {
				t.Fatalf("second round = (%+v, %v), want a created run", second, err)
			}
			persisted, ok, err := st.GetReviewBySessionAndHarness(ctx, worker.ID, domain.ReviewerClaudeCode)
			if err != nil || !ok {
				t.Fatalf("get review: ok=%v err=%v", ok, err)
			}
			if tc.resume {
				if got := argAfter(rt.createCfg.Argv, "--resume"); got != firstID {
					t.Fatalf("second round --resume = %q, want %q (argv %#v)", got, firstID, rt.createCfg.Argv)
				}
				if persisted.AgentSessionID != firstID {
					t.Fatalf("persisted agent session id = %q, want %q", persisted.AgentSessionID, firstID)
				}
				return
			}
			secondID := argAfter(rt.createCfg.Argv, "--session-id")
			if secondID == "" || secondID == firstID {
				t.Fatalf("second round --session-id = %q, want a new conversation distinct from %q (argv %#v)", secondID, firstID, rt.createCfg.Argv)
			}
			if persisted.AgentSessionID != secondID {
				t.Fatalf("persisted agent session id = %q, want launched id %q", persisted.AgentSessionID, secondID)
			}
		})
	}
}

// stubClaudeReviewer puts a stub claude binary on PATH and points Claude's
// config dir at a temp dir, returning that dir.
func stubClaudeReviewer(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binaryName := "claude"
	if runtime.GOOS == "windows" {
		binaryName = "claude.exe"
	}
	if err := os.WriteFile(filepath.Join(binDir, binaryName), []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	return configDir
}

func writeClaudeTranscript(t *testing.T, configDir, id string) {
	t.Helper()
	projectDir := filepath.Join(configDir, "projects", "reviewer-workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func argAfter(argv []string, flag string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			return argv[i+1]
		}
	}
	return ""
}
