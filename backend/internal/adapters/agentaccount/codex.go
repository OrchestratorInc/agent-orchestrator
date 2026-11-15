package agentaccount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var codexKnownShared = []string{
	"sessions", "archived_sessions", "sqlite", "shell_snapshots", "worktrees",
	"skills", "plugins", "cache", "logs", "mcp-oauth-locks", "thread-writer-locks",
}

var codexKnownSharedFiles = []string{"config.toml", "history.jsonl"}

var codexPrivate = map[string]bool{
	"auth.json": true, "models_cache.json": true,
	"log": true, "memories": true, "tmp": true, ".tmp": true,
	"app-server-control": true, "app-server-daemon": true, "ipc": true,
	".codex-global-state.json": true, ".codex-global-state.json.bak": true,
}

var codexReplaceable = map[string]bool{"mcp-oauth-locks": true, ".sqlite-maintenance.lock": true}

var codexCredentialEnv = []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"}

type Codex struct {
	Binary     func(context.Context) (string, error)
	Accounts   ports.CodexAccountClientFactory
	DeviceHome string
}

var _ ports.AgentAccountProviderCLI = (*Codex)(nil)

func (*Codex) Provider() domain.AgentAccountProvider { return domain.AgentAccountProviderCodex }

func (*Codex) LaunchEnv(home string) map[string]string {
	if home == "" {
		return nil
	}
	env := map[string]string{"CODEX_HOME": home}
	neutralize(env, codexCredentialEnv...)
	return env
}

func (c *Codex) LoginArgv(ctx context.Context) ([]string, error) {
	bin, err := c.Binary(ctx)
	if err != nil {
		return nil, err
	}
	return []string{bin, "login", "--device-auth"}, nil
}

func (c *Codex) open(ctx context.Context, home string) (ports.CodexAccountClient, error) {
	if home == "" {
		home = c.DeviceHome
	}
	return c.Accounts.Open(ctx, ports.CodexAccountContext{Home: home})
}

func (c *Codex) Logout(ctx context.Context, home string) error {
	client, err := c.open(ctx, home)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return client.Logout(ctx)
}

func (c *Codex) Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error) {
	client, err := c.open(ctx, home)
	if err != nil {
		return domain.AgentAccountIdentityCheck{}, err
	}
	defer func() { _ = client.Close() }()
	account, err := client.Read(ctx, false)
	if err != nil {
		return domain.AgentAccountIdentityCheck{}, err
	}
	out := domain.AgentAccountIdentityCheck{
		SignedIn: account.Authentication == domain.AgentAuthenticationAuthorized && account.Method == domain.CodexAuthMethodChatGPT,
		LoggedIn: account.Authentication == domain.AgentAuthenticationAuthorized,
	}
	if account.Email != nil {
		out.Email = *account.Email
	}
	if out.SignedIn {
		if capacity, err := client.ReadCapacity(ctx); err == nil && capacity.Plan != nil {
			out.Plan = *capacity.Plan
		}
	}
	return out, nil
}

func (c *Codex) Limits(ctx context.Context, home string) (domain.AgentAccountLimits, error) {
	client, err := c.open(ctx, home)
	if err != nil {
		return domain.AgentAccountLimits{}, err
	}
	defer func() { _ = client.Close() }()
	capacity, err := client.ReadCapacity(ctx)
	if err != nil {
		return domain.AgentAccountLimits{}, err
	}
	out := domain.AgentAccountLimits{
		Windows: []domain.AgentAccountLimitWindow{}, FetchedAt: time.Now().UTC(),
		AutoResumeResetsAt: capacity.AutoResumeResetsAt,
	}
	addBucket := func(bucket *domain.CodexCapacityBucket, prefix string) {
		if bucket == nil {
			return
		}
		if bucket.Reached == domain.CodexCapacityReached {
			out.ReachedScopes = append(out.ReachedScopes, prefix)
		}
		for _, w := range []*domain.CodexCapacityWindow{bucket.Primary, bucket.Secondary} {
			if w == nil {
				continue
			}
			label := codexWindowLabel(w.WindowDurationMinutes)
			if prefix != "" {
				label = prefix + " · " + label
			}
			window := domain.AgentAccountLimitWindow{
				ID: bucket.LimitID + ":" + label, Label: label, UsedPercent: min(max(w.UsedPercent, 0), 100), ResetsAt: w.ResetsAt, Scope: prefix,
			}
			if w.WindowDurationMinutes != nil && *w.WindowDurationMinutes > 0 {
				window.DurationMinutes = int(*w.WindowDurationMinutes)
			}
			out.Windows = append(out.Windows, window)
		}
	}
	addBucket(capacity.Overall, "")
	for i := range capacity.AdditionalBuckets {
		bucket := capacity.AdditionalBuckets[i]
		name := bucket.LimitID
		if bucket.DisplayName != nil && strings.TrimSpace(*bucket.DisplayName) != "" {
			name = *bucket.DisplayName
		}
		addBucket(&bucket, name)
	}
	if capacity.ResetCredits != nil {
		n := capacity.ResetCredits.AvailableCount
		out.ResetCredits = &n
	}
	if usage, err := client.ReadUsage(ctx); err == nil {
		out.Activity = &domain.AgentAccountActivity{
			LatestDayTokens: usage.LatestDayTokens, LatestDay: usage.LatestDayStartDate, LifetimeTokens: usage.LifetimeTokens,
			PeakDailyTokens: usage.PeakDailyTokens, LongestTurnSeconds: usage.LongestRunningTurnSeconds, CurrentStreakDays: usage.CurrentStreakDays,
		}
	}
	return out, nil
}

func (c *Codex) ConsumeResetCredit(ctx context.Context, home, idempotencyKey string) (domain.CodexResetCreditOutcome, error) {
	client, err := c.open(ctx, home)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()
	return client.ConsumeResetCredit(ctx, idempotencyKey)
}

func codexWindowLabel(minutes *int64) string {
	switch {
	case minutes == nil:
		return "Limit"
	case *minutes <= 5*60:
		return "Session (5h)"
	case *minutes >= 7*24*60:
		return "Weekly"
	default:
		return fmt.Sprintf("%dh", *minutes/60)
	}
}

func (c *Codex) Prepare(home string) error {
	if home == "" {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	names := map[string]bool{}
	for _, name := range codexKnownShared {
		names[name] = false
	}
	for _, name := range codexKnownSharedFiles {
		names[name] = true
	}
	entries, err := os.ReadDir(c.DeviceHome)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if _, known := names[entry.Name()]; !known {
			names[entry.Name()] = !entry.IsDir()
		}
	}
	pruneDangling(home, c.DeviceHome, names)
	var errs []error
	for name, file := range names {
		if codexPrivate[name] {
			continue
		}
		errs = append(errs, linkShared(home, c.DeviceHome, name, file, codexReplaceable[name]))
	}
	return errors.Join(errs...)
}
