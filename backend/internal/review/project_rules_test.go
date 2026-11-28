package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type ruleFileFunc func(ctx context.Context, repo, branch, path string) (string, error)

func (f ruleFileFunc) ReadDefaultBranchFile(ctx context.Context, repo, branch, path string) (string, error) {
	return f(ctx, repo, branch, path)
}

func TestTriggerDeliversProjectRulesInEveryNewPass(t *testing.T) {
	ctx := context.Background()
	projectRoot := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const fileRule = "Format color labels in lowercase."
	const inlineRule = "Use violet for the accent label."
	for _, mode := range []domain.ReviewerInterfaceMode{domain.ReviewerInterfaceChat, domain.ReviewerInterfaceTUI} {
		t.Run(string(mode), func(t *testing.T) {
			worker := liveWorker()
			worker.Metadata.WorkspacePath = t.TempDir()
			store := newSQLiteReviewStore(t)
			seedReviewWorker(t, store, worker)
			dataDir := t.TempDir()
			terminal := &fakeRuntime{alive: true, supervisedRecord: true}
			terminalReviewer := &fakeReviewer{}
			chat := &ruleTestChat{sqliteReviewChatController: sqliteReviewChatController{store: store, started: new(ReviewerChatStart)}, alive: true}
			adapter := ruleTestAdapter{reviewerCommandFunc{
				reviewer: terminalReviewer,
				reviewCommand: func(_ context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
					terminalReviewer.gotInv = inv
					return ports.ReviewCommandSpec{Argv: []string{executable}, AgentSessionID: "fixture-terminal"}, nil
				},
			}}
			launcher := NewLauncher(singleReviewerResolver{reviewer: adapter}, terminal, dataDir, WithReviewerChat(chat))
			project := &fakeProjects{path: projectRoot, cfg: domain.ProjectConfig{
				DefaultBranch: "upstream/main", AgentRules: inlineRule, AgentRulesFile: "guide/colors.md",
			}}
			eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), project, launcher)
			eng.ruleFiles = ruleFileFunc(func(_ context.Context, repo, branch, path string) (string, error) {
				if repo != projectRoot || branch != "upstream/main" || path != "guide/colors.md" {
					t.Fatalf("rules source = %q, %q, %q", repo, branch, path)
				}
				return "\n" + fileRule + "\n", nil
			})
			launchPass := func(head string) string {
				t.Helper()
				eng.prs = prAt(head)
				res, err := eng.TriggerWithOptions(ctx, worker.ID, TriggerOptions{InterfaceMode: mode})
				if err != nil || !res.Created {
					t.Fatalf("trigger %s = created %v, %v", head, res.Created, err)
				}
				taskPath := filepath.Join(dataDir, "prompts", string(worker.ID), "reviewer", "requests", res.Run.BatchID, res.Run.ID, "task.md")
				body, err := os.ReadFile(taskPath)
				if err != nil {
					t.Fatal(err)
				}
				var delivered string
				if mode == domain.ReviewerInterfaceChat {
					delivered = chat.lastMessage
					if delivered == "" {
						delivered = chat.started.Prompt
					}
				} else {
					delivered = terminalReviewer.gotInv.Prompt
				}
				if !strings.Contains(delivered, filepath.ToSlash(taskPath)) {
					t.Fatal("reviewer did not receive this pass's task file")
				}
				return string(body)
			}
			if task := launchPass("sha1"); !strings.Contains(task, inlineRule+"\n\n"+fileRule) {
				t.Fatal("fresh reviewer did not receive both project rules")
			}

			project.cfg.AgentRules = ""
			project.cfg.AgentRulesFile = ""
			if task := launchPass("sha2"); strings.Contains(task, inlineRule) || strings.Contains(task, fileRule) {
				t.Fatal("new pass used rules removed from project config")
			}
			if mode == domain.ReviewerInterfaceChat && chat.lastMessage == "" {
				t.Fatal("expected the second pass to notify the existing Chat reviewer")
			}
			if mode == domain.ReviewerInterfaceTUI && terminal.sentMsg == "" {
				t.Fatal("expected the second pass to notify the existing terminal reviewer")
			}

			chat.alive, terminal.alive = false, false
			restored, err := eng.RestoreReviewer(ctx, worker.ID)
			if err != nil || !restored.Restored {
				t.Fatalf("restore reviewer = %v, %v", restored.Restored, err)
			}
			chat.lastMessage = ""
			chat.alive, terminal.alive = true, true
			const updatedRule = "Use amber for the accent label."
			project.cfg.AgentRules = updatedRule
			if task := launchPass("sha3"); !strings.Contains(task, updatedRule) || strings.Contains(task, inlineRule) || strings.Contains(task, fileRule) {
				t.Fatal("the restored reviewer did not receive the current inline rules")
			}
		})
	}
}

type ruleTestAdapter struct{ reviewerCommandFunc }

func (ruleTestAdapter) ReviewChatHarness() domain.AgentHarness { return domain.HarnessClaudeCode }

type ruleTestChat struct {
	sqliteReviewChatController
	lastMessage string
	alive       bool
}

func (c *ruleTestChat) ReviewChatAlive(string) bool { return c.alive }

func (c *ruleTestChat) RestoreReviewChat(_ context.Context, cfg ReviewerChatStart) (string, error) {
	*c.started = cfg
	return "provider-conversation", nil
}

func (c *ruleTestChat) SendReviewChat(_ context.Context, _, message, _ string) error {
	c.lastMessage = message
	return nil
}

func TestTriggerFailsConfiguredRulesPreflightBeforeDispatch(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"),
		fakeProjects{cfg: domain.ProjectConfig{AgentRules: "Use violet for the accent label.", AgentRulesFile: "guide/colors.md"}}, launcher)
	missing := errors.New("rules file missing")
	eng.ruleFiles = ruleFileFunc(func(context.Context, string, string, string) (string, error) {
		return "", missing
	})
	_, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if !errors.Is(err, missing) || !strings.Contains(err.Error(), "agentRulesFile") {
		t.Fatalf("trigger error = %v; want a configured file error", err)
	}
	if launcher.spawned || launcher.notified || len(store.runs) != 1 || store.runs[0].FailureReason != domain.ReviewFailurePreflightFailed {
		t.Fatalf("expected a failed preflight before reviewer dispatch, got runs %+v", store.runs)
	}
}
