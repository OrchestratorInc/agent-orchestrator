package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/mentionspawn"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// mentionSpawnEnv turns on starting an agent from an "@ao" comment under a PR
// no session owns — typically one a person opened by hand.
const mentionSpawnEnv = "AO_PR_MENTION_SPAWN"

// startMentionSpawn wires the opt-in mention-spawn loop. Off unless
// AO_PR_MENTION_SPAWN is truthy: it starts agents on branches people push to,
// which a deployment has to choose.
func startMentionSpawn(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, announcer mentionspawn.Announcer, logger *slog.Logger) <-chan struct{} {
	if !mentionSpawnEnabled(logger) {
		return closedDone()
	}
	provider, err := newGitHubSCMProvider(logger)
	if err != nil {
		logger.Warn("mention spawn disabled: GitHub provider setup failed", "err", err)
		return closedDone()
	}
	logger.Info("mention spawn enabled: an @-mention under a PR without a session starts an agent for it")
	observer := mentionspawn.New(provider, store, mentionSessions{sessions}, gitBranchFetcher{}, mentionspawn.Config{
		Logger:    logger,
		Announcer: announcer,
	})
	return observer.Start(ctx)
}

func mentionSpawnEnabled(logger *slog.Logger) bool {
	raw := strings.TrimSpace(os.Getenv(mentionSpawnEnv))
	if raw == "" {
		return false
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		logger.Warn(mentionSpawnEnv+" is not a boolean, mention spawn stays off", "value", raw)
		return false
	}
	return on
}

// mentionSessions adapts the session service to the loop's narrow surface.
type mentionSessions struct {
	svc *sessionsvc.Service
}

func (m mentionSessions) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	return m.svc.Spawn(ctx, cfg)
}

// ClaimPR never takes a PR from a live session: the loop only gets here for a
// PR without one, and a race with a human claim must not steal it back.
func (m mentionSessions) ClaimPR(ctx context.Context, id domain.SessionID, prURL string) error {
	_, err := m.svc.ClaimPR(ctx, id, prURL, sessionsvc.ClaimPROptions{AllowTakeover: false})
	return err
}

// fetchBudget bounds one branch fetch; a hung remote must not hold the loop.
const fetchBudget = 2 * time.Minute

// gitBranchFetcher updates origin/<branch> in the project checkout so the
// worktree for the new session starts from the PR's head, not the default
// branch.
type gitBranchFetcher struct{}

func (gitBranchFetcher) FetchBranch(ctx context.Context, repoPath, branch string) error {
	if !safeBranchName(branch) {
		return fmt.Errorf("refusing to fetch branch %q", branch)
	}
	runCtx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	cmd := aoprocess.CommandContext(runCtx, "git", "-C", repoPath, "fetch", "--no-tags", "origin", refspec)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return err
		}
		return errors.New(firstLines(text, 4))
	}
	return nil
}

// safeBranchName rejects names git would read as an option or that cannot be a
// plain branch ref. GitHub already refuses most of these; the check keeps a
// hostile name from ever reaching the command line.
func safeBranchName(branch string) bool {
	if branch == "" || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		return false
	}
	return !strings.ContainsAny(branch, " \t\n:~^?*[\\")
}
