package agentaccount

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var claudeSharedDirs = []string{
	"projects", "sessions", "session-env", "file-history", "shell-snapshots", "todos",
	"paste-cache", "tasks", "plans", "transcripts",
	"skills", "plugins", "agents", "commands", "output-styles", "hooks",
}

var claudeSettingsDropped = []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "forceLoginMethod", "forceLoginOrgUUID"}

var claudeCredentialEnv = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
}

type Claude struct {
	Binary       func(context.Context) (string, error)
	DeviceHome   string
	DeviceConfig string
	DeviceEnv    func(context.Context) map[string]string
}

var _ ports.AgentAccountProviderCLI = (*Claude)(nil)

func (*Claude) Provider() domain.AgentAccountProvider { return domain.AgentAccountProviderClaude }

func (*Claude) LaunchEnv(home string) map[string]string {
	if home == "" {
		return nil
	}
	env := map[string]string{"CLAUDE_CONFIG_DIR": home, "CLAUDE_SECURESTORAGE_CONFIG_DIR": home}
	neutralize(env, claudeCredentialEnv...)
	return env
}

func (c *Claude) LoginArgv(ctx context.Context) ([]string, error) {
	bin, err := c.Binary(ctx)
	if err != nil {
		return nil, err
	}
	return []string{bin, "auth", "login"}, nil
}

func (c *Claude) Logout(ctx context.Context, home string) error {
	bin, err := c.Binary(ctx)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, bin, home, nil, "auth", "logout")
	return err
}

func (c *Claude) Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error) {
	bin, err := c.Binary(ctx)
	if err != nil {
		return domain.AgentAccountIdentityCheck{}, err
	}
	out, runErr := c.run(ctx, bin, home, nil, "auth", "status", "--json")
	var report struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		Email            string `json:"email"`
		SubscriptionType string `json:"subscriptionType"`
		OrgID            string `json:"orgId"`
		OrgName          string `json:"orgName"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &report); err != nil {
		if runErr != nil {
			return domain.AgentAccountIdentityCheck{}, runErr
		}
		return domain.AgentAccountIdentityCheck{}, fmt.Errorf("read claude auth status: %w", err)
	}
	subscription := report.AuthMethod == "claude.ai" || report.AuthMethod == "oauth_token"
	return domain.AgentAccountIdentityCheck{
		SignedIn: report.LoggedIn && subscription,
		LoggedIn: report.LoggedIn,
		Email:    report.Email,
		Plan:     report.SubscriptionType,
		OrgID:    report.OrgID,
		OrgName:  report.OrgName,
	}, nil
}

func (c *Claude) Limits(ctx context.Context, home string) (domain.AgentAccountLimits, error) {
	bin, err := c.Binary(ctx)
	if err != nil {
		return domain.AgentAccountLimits{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request := `{"type":"control_request","request_id":"ao-usage","request":{"subtype":"get_usage","skip_behaviors":true}}` + "\n"
	cmd := c.command(ctx, bin, home, "--output-format", "stream-json", "--verbose", "--input-format", "stream-json")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return domain.AgentAccountLimits{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return domain.AgentAccountLimits{}, err
	}
	if err := cmd.Start(); err != nil {
		return domain.AgentAccountLimits{}, fmt.Errorf("start claude: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Cancel()
		_ = cmd.Wait()
	}()
	if _, err := stdin.Write([]byte(request)); err != nil {
		return domain.AgentAccountLimits{}, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		var line struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Error     string          `json:"error"`
				Response  claudeUsageBody `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Type != "control_response" || line.Response.RequestID != "ao-usage" {
			continue
		}
		if line.Response.Subtype != "success" {
			return domain.AgentAccountLimits{}, fmt.Errorf("claude get_usage: %s", strings.TrimSpace(line.Response.Error))
		}
		body := line.Response.Response
		if body.RateLimitsAvailable && body.RateLimits == nil {
			return domain.AgentAccountLimits{}, errors.New("claude could not fetch its usage limits")
		}
		return body.limits(time.Now().UTC()), nil
	}
	if err := ctx.Err(); err != nil {
		return domain.AgentAccountLimits{}, err
	}
	return domain.AgentAccountLimits{}, errors.New("claude ended without answering get_usage")
}

type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type claudeUsageBody struct {
	RateLimitsAvailable bool `json:"rate_limits_available"`
	RateLimits          *struct {
		FiveHour    *claudeUsageWindow `json:"five_hour"`
		SevenDay    *claudeUsageWindow `json:"seven_day"`
		Opus        *claudeUsageWindow `json:"seven_day_opus"`
		Sonnet      *claudeUsageWindow `json:"seven_day_sonnet"`
		ModelScoped []struct {
			DisplayName string `json:"display_name"`
			claudeUsageWindow
		} `json:"model_scoped"`
		Spend *struct {
			Enabled bool         `json:"enabled"`
			Used    *claudeMoney `json:"used"`
			Limit   *claudeMoney `json:"limit"`
		} `json:"spend"`
	} `json:"rate_limits"`
}

type claudeMoney struct {
	AmountMinor float64 `json:"amount_minor"`
	Currency    string  `json:"currency"`
	Exponent    int     `json:"exponent"`
}

func (m *claudeMoney) major() *float64 {
	if m == nil {
		return nil
	}
	v := m.AmountMinor / math.Pow10(m.Exponent)
	return &v
}

func (b claudeUsageBody) limits(now time.Time) domain.AgentAccountLimits {
	out := domain.AgentAccountLimits{Windows: []domain.AgentAccountLimitWindow{}, FetchedAt: now}
	r := b.RateLimits
	if r == nil {
		return out
	}
	add := func(id, label, scope string, w *claudeUsageWindow) {
		if w == nil || w.Utilization == nil {
			return
		}
		window := domain.AgentAccountLimitWindow{ID: id, Label: label, UsedPercent: min(max(*w.Utilization, 0), 100), DurationMinutes: 7 * 24 * 60, Scope: scope}
		if id == "five_hour" {
			window.DurationMinutes = 5 * 60
		}
		if w.ResetsAt != nil {
			if at, err := time.Parse(time.RFC3339, *w.ResetsAt); err == nil {
				window.ResetsAt = &at
			}
		}
		out.Windows = append(out.Windows, window)
	}
	add("five_hour", "Session (5h)", "", r.FiveHour)
	add("seven_day", "Weekly", "", r.SevenDay)
	add("seven_day_opus", "Weekly · Opus", "opus", r.Opus)
	add("seven_day_sonnet", "Weekly · Sonnet", "sonnet", r.Sonnet)
	for _, scoped := range r.ModelScoped {
		w := scoped.claudeUsageWindow
		add("model:"+scoped.DisplayName, "Weekly · "+scoped.DisplayName, scoped.DisplayName, &w)
	}
	out.AutoResumeResetsAt = b.autoResumeResetAt(now)
	if e := r.Spend; e != nil {
		out.ExtraUsage = &domain.AgentAccountExtraUsage{Enabled: e.Enabled, UsedCredits: e.Used.major(), MonthlyLimit: e.Limit.major()}
		if e.Used != nil {
			out.ExtraUsage.Currency = e.Used.Currency
		}
	}
	return out
}

func (b claudeUsageBody) autoResumeResetAt(now time.Time) *time.Time {
	r := b.RateLimits
	if r == nil || (r.Spend != nil && r.Spend.Enabled) {
		return nil
	}
	valid := func(w *claudeUsageWindow) bool {
		return w != nil && w.Utilization != nil && !math.IsNaN(*w.Utilization) &&
			!math.IsInf(*w.Utilization, 0) && *w.Utilization >= 0 && *w.Utilization <= 100
	}
	models := []*claudeUsageWindow{r.Opus, r.Sonnet}
	for i := range r.ModelScoped {
		models = append(models, &r.ModelScoped[i].claudeUsageWindow)
	}
	for _, w := range models {
		if w != nil && (!valid(w) || *w.Utilization >= 100) {
			return nil
		}
	}
	var latest time.Time
	for _, w := range []*claudeUsageWindow{r.FiveHour, r.SevenDay} {
		if w == nil {
			continue
		}
		if !valid(w) {
			return nil
		}
		if *w.Utilization < 100 {
			continue
		}
		if w.ResetsAt == nil {
			return nil
		}
		reset, err := time.Parse(time.RFC3339, *w.ResetsAt)
		if err != nil || !reset.After(now) {
			return nil
		}
		if reset.After(latest) {
			latest = reset.UTC()
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

func (c *Claude) Prepare(home string) error {
	if home == "" {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{"history.jsonl": true}
	for _, dir := range claudeSharedDirs {
		keep[dir] = true
	}
	pruneDangling(home, c.DeviceHome, keep)
	var errs []error
	for _, dir := range claudeSharedDirs {
		errs = append(errs, linkShared(home, c.DeviceHome, dir, false, false))
	}
	errs = append(errs, linkShared(home, c.DeviceHome, "history.jsonl", true, false))
	if matches, err := filepath.Glob(filepath.Join(c.DeviceHome, "*.md")); err == nil {
		for _, match := range matches {
			errs = append(errs, linkShared(home, c.DeviceHome, filepath.Base(match), true, false))
		}
	}
	l := readLedger(home)
	errs = append(errs, c.shareSettings(home, l), c.shareGlobalConfig(home, l), l.save())
	return errors.Join(errs...)
}

func (c *Claude) shareSettings(home string, l *ledger) error {
	source, err := readObject(filepath.Join(c.DeviceHome, "settings.json"))
	if err != nil {
		return err
	}
	path := filepath.Join(home, "settings.json")
	account, err := readObject(path)
	if err != nil {
		return err
	}
	dropLoginKeys(source)
	changed := shareKeys(account, source, l.keys("settings.json", account, source), map[string]bool{"env": true})
	if dropLoginKeys(account) {
		changed = true
	}
	if !changed {
		return nil
	}
	return writeObject(path, account)
}

func dropLoginKeys(settings map[string]json.RawMessage) bool {
	dropped := false
	for _, key := range claudeSettingsDropped {
		if _, ok := settings[key]; ok {
			delete(settings, key)
			dropped = true
		}
	}
	if raw, ok := settings["env"]; ok {
		env := object(raw)
		for _, key := range claudeCredentialEnv {
			if _, ok := env[key]; ok {
				delete(env, key)
				dropped = true
			}
		}
		settings["env"] = encode(env)
	}
	return dropped
}

func (c *Claude) shareGlobalConfig(home string, l *ledger) error {
	path := filepath.Join(home, ".claude.json")
	account, err := readObject(path)
	if err != nil {
		return err
	}
	changed := false
	if device, err := readObject(c.DeviceConfig); err == nil {
		source := map[string]json.RawMessage{}
		for _, key := range []string{"mcpServers", "theme"} {
			if value, ok := device[key]; ok {
				source[key] = value
			}
		}
		changed = shareKeys(account, source, l.keys(".claude.json", account, source), map[string]bool{"mcpServers": true})
	}
	if _, ok := account["hasCompletedOnboarding"]; !ok {
		account["hasCompletedOnboarding"], changed = json.RawMessage("true"), true
	}
	if !changed {
		return nil
	}
	return writeObject(path, account)
}

func (c *Claude) command(ctx context.Context, bin, home string, args ...string) *exec.Cmd {
	cmd := aoprocess.CommandContext(ctx, bin, args...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Env = os.Environ()
	for key, value := range c.LaunchEnv(home) {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	if home == "" && c.DeviceEnv != nil {
		for key, value := range c.DeviceEnv(ctx) {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Dir = os.TempDir()
	return cmd
}

func (c *Claude) run(ctx context.Context, bin, home string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := c.command(ctx, bin, home, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, err
}
