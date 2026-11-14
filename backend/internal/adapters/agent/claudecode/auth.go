package claudecode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

const claudeAuthProbeTimeout = 3 * time.Second

var claudeCredentialEnv = []string{
	"CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
}

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	return p.authStatusFor(ctx, "", nil, false)
}

func credentialEvidence(credential agentcreds.Credential) domain.AgentAccountIdentity {
	method := domain.AgentAccountMethodAccount
	switch credential.Kind {
	case agentcreds.KindAPIKey:
		method = domain.AgentAccountMethodAPIKey
	case agentcreds.KindOAuthToken, agentcreds.KindAuthToken:
		method = domain.AgentAccountMethodToken
	}
	switch source := strings.TrimSpace(credential.Source); {
	case source == "keychain":
		return domain.KeychainSource().WithMethod(domain.AgentAccountMethodAccount)
	case source == "credentials-file":
		return authprobe.FileSource(filepath.Join(claudeConfigDir(), ".credentials.json")).WithMethod(domain.AgentAccountMethodAccount)
	case envVarName.MatchString(source):
		return domain.EnvironmentSource(source).WithMethod(method)
	default:
		return domain.AgentAccountIdentity{Method: method}
	}
}

var envVarName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func claudeConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func (p *Plugin) ValidateLaunchAuth(ctx context.Context, workingDir string, env map[string]string) (ports.AgentAuthStatus, error) {
	return p.authStatusFor(ctx, workingDir, env, false)
}

func (p *Plugin) authStatusFor(ctx context.Context, workingDir string, env map[string]string, fresh bool) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.authStatusAndDiagnostics(ctx, workingDir, env, fresh)
	return status, err
}

func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	status, report, steps, err := p.authStatusAndDiagnostics(ctx, "", nil, false)
	return status, report.account(), steps, err
}

func (p *Plugin) AuthStatusForRuntime(ctx context.Context, dataDir string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	env := make(map[string]string)
	for name, value := range ports.AuthEnvironmentOverrides(ctx) {
		env[name] = value
	}
	p.AugmentRuntimeEnv(env, dataDir)
	status, report, steps, err := p.authStatusAndDiagnostics(ctx, "", env, false)
	return status, report.account(), steps, err
}

func (p *Plugin) authStatusAndDiagnostics(ctx context.Context, workingDir string, env map[string]string, fresh bool) (ports.AgentAuthStatus, claudeAuthReport, []domain.AgentAuthDiagnostic, error) {
	binary, err := p.claudeBinary(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return ports.AgentAuthStatusUnknown, claudeAuthReport{}, nil, nil
		}
		return ports.AgentAuthStatusUnknown, claudeAuthReport{}, nil, err
	}

	var steps []domain.AgentAuthDiagnostic
	resolved := p.resolveProviderContext(ctx, binary, workingDir, env, func(ctx context.Context, binary, cwd string, env map[string]string) (claudeAuthReport, bool) {
		report, ok, step := p.claudeCLIAuthReportDiagnostic(ctx, binary, cwd, env)
		steps = append(steps, step)
		return report, ok
	})
	reportedProvider, _ := agentcreds.ParseProvider(resolved.report.APIProvider)
	reportApplies := resolved.cliOK && (resolved.provider == agentcreds.ProviderFirstParty || resolved.provider == reportedProvider)
	report := claudeAuthReport{}
	if reportApplies {
		report = resolved.report
	}

	if resolved.found {
		evidence := credentialEvidence(resolved.credential)
		if evidence.Method != domain.AgentAccountMethodAccount {
			report.AuthMethod = ""
		}
		ports.RecordAuthEvidence(ctx, evidence)
	} else if reportApplies {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "auth", "status"))
	}

	status, ok, step := p.probeResolvedAuthDiagnostics(ctx, resolved, fresh)
	steps = append(steps, step)
	if ok {
		return status, report, steps, nil
	}

	if reportApplies {
		status := resolved.report.status()
		if status != ports.AgentAuthStatusUnknown {
			return status, report, steps, nil
		}
	}

	status, err = claudeLocalAuthStatus(ctx, resolved.opts)
	return status, report, steps, err
}

type claudeProviderContext struct {
	opts       agentcreds.ResolveOptions
	report     claudeAuthReport
	cliOK      bool
	provider   agentcreds.Provider
	providerOK bool
	credential agentcreds.Credential
	found      bool
}

func (p *Plugin) resolveProviderContext(
	ctx context.Context,
	binary, workingDir string,
	env map[string]string,
	reportFn func(context.Context, string, string, map[string]string) (claudeAuthReport, bool),
) claudeProviderContext {
	opts := (agentcreds.ResolveOptions{
		AllowKeychain: true,
		WorkingDir:    workingDir,
		CommandEnv:    env,
	}).WithClaudeSettings(ctx)
	report, cliOK := reportFn(ctx, binary, workingDir, opts.CommandEnv)
	reported := ""
	if cliOK {
		reported = report.APIProvider
	}
	provider, providerOK := agentcreds.ResolveProvider(reported, opts)
	credential, found := agentcreds.Credential{}, false
	if providerOK {
		credential, found = agentcreds.ResolveLocal(ctx, provider, opts)
	}
	return claudeProviderContext{
		opts: opts, report: report, cliOK: cliOK, provider: provider,
		providerOK: providerOK, credential: credential, found: found,
	}
}

func (p *Plugin) probeResolvedAuthStatus(ctx context.Context, resolved claudeProviderContext, fresh bool) (ports.AgentAuthStatus, bool) {
	status, ok, _ := p.probeResolvedAuthDiagnostics(ctx, resolved, fresh)
	return status, ok
}

func (p *Plugin) probeResolvedAuthDiagnostics(ctx context.Context, resolved claudeProviderContext, fresh bool) (ports.AgentAuthStatus, bool, domain.AgentAuthDiagnostic) {
	diagnostic := func(code string) domain.AgentAuthDiagnostic {
		d := domain.NewAgentAuthDiagnostic("provider_validation", code)
		d.Provider = string(resolved.provider)
		d.Source = resolved.credential.Source
		return d
	}
	if !resolved.providerOK || !agentcreds.ValidatesProvider(resolved.provider) {
		return ports.AgentAuthStatusUnknown, false, diagnostic("provider_unsupported")
	}
	if resolved.found && resolved.credential.ExpiredButRenewable(claudeNow()) {
		return ports.AgentAuthStatusConfigured, true, diagnostic("token_renewal_pending")
	}
	if !resolved.found {
		return ports.AgentAuthStatusUnknown, false, diagnostic("credential_missing")
	}
	var result agentcreds.Result
	cached := false
	if !fresh {
		result, cached = p.authCache().get(resolved.credential.Fingerprint(), resolved.provider)
	}
	started := time.Now()
	if !cached {
		probeCtx, cancel := context.WithTimeout(ctx, agentcreds.DefaultTimeout)
		defer cancel()
		result = claudeValidator().ValidateResolvedLocal(probeCtx, resolved.provider, resolved.credential, resolved.found, resolved.opts)
		p.authCache().put(result)
	}
	code := "provider_inconclusive"
	switch {
	case result.State == agentcreds.StateValid:
		code = "provider_accepted"
	case result.State == agentcreds.StateInvalid:
		code = "provider_rejected"
	case errors.Is(result.Err, context.DeadlineExceeded):
		code = "probe_timeout"
	case errors.Is(result.Err, context.Canceled):
		code = "probe_cancelled"
	case result.Err != nil && result.HTTPStatus == 0:
		code = "provider_unreachable"
	}
	step := diagnostic(code)
	step.CheckedAt = result.CheckedAt
	step.HTTPStatus = result.HTTPStatus
	step.Cached = cached
	step.DurationMs = time.Since(started).Milliseconds()
	return authStatusFromResult(result), result.State != agentcreds.StateUnknown, step
}

func (p *Plugin) probeAuthStatus(ctx context.Context, report claudeAuthReport, opts agentcreds.ResolveOptions) (ports.AgentAuthStatus, bool) {
	reported := report.APIProvider
	provider, providerOK := agentcreds.ResolveProvider(reported, opts)
	credential, found := agentcreds.Credential{}, false
	if providerOK {
		credential, found = agentcreds.ResolveLocal(ctx, provider, opts)
	}
	return p.probeResolvedAuthStatus(ctx, claudeProviderContext{
		opts: opts, report: report, cliOK: true, provider: provider,
		providerOK: providerOK, credential: credential, found: found,
	}, false)
}

func authStatusFromResult(result agentcreds.Result) ports.AgentAuthStatus {
	switch result.State {
	case agentcreds.StateValid:
		return ports.AgentAuthStatusAuthorized
	case agentcreds.StateInvalid:
		return ports.AgentAuthStatusUnauthorized
	default:
		return ports.AgentAuthStatusUnknown
	}
}

var claudeValidator = func() *agentcreds.Validator { return agentcreds.New(nil) }

func (p *Plugin) authCache() *authCache { return claudeAuthCache }

var claudeAuthCache = newAuthCache(defaultAuthCacheTTL)

var claudeNow = time.Now

func InvalidateAuthCache() { claudeAuthCache.invalidate() }

func (p *Plugin) InvalidateAuthCache() { InvalidateAuthCache() }

type claudeAuthReport struct {
	LoggedIn         *bool  `json:"loggedIn"`
	APIProvider      string `json:"apiProvider"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

func (r claudeAuthReport) account() domain.AgentAccountIdentity {
	if r.LoggedIn == nil || !*r.LoggedIn {
		return domain.AgentAccountIdentity{}
	}
	identity := domain.AgentAccountIdentity{Email: r.Email, Organization: r.OrgName, Plan: r.SubscriptionType}
	method := strings.ToLower(strings.TrimSpace(r.AuthMethod))
	switch {
	case method == "claude.ai" || strings.Contains(method, "oauth"):
		identity.Method = domain.AgentAccountMethodAccount
	case strings.Contains(method, "key"):
		identity.Method = domain.AgentAccountMethodAPIKey
	}
	return identity
}

func (r claudeAuthReport) status() ports.AgentAuthStatus {
	if r.LoggedIn == nil {
		return ports.AgentAuthStatusUnknown
	}
	if *r.LoggedIn {
		return ports.AgentAuthStatusConfigured
	}
	return ports.AgentAuthStatusUnauthorized
}

func (p *Plugin) claudeCLIAuthReport(ctx context.Context, binary, workingDir string, env map[string]string) (claudeAuthReport, bool) {
	report, ok, _ := p.claudeCLIAuthReportDiagnostic(ctx, binary, workingDir, env)
	return report, ok
}

func (p *Plugin) claudeCLIAuthReportDiagnostic(ctx context.Context, binary, workingDir string, env map[string]string) (claudeAuthReport, bool, domain.AgentAuthDiagnostic) {
	started := time.Now()
	key := authReportCacheKey(binary, workingDir, env)
	if report, hit := claudeAuthReports.get(key); hit {
		step := authprobe.CommandStep(binary, []string{"auth", "status"}, report.status(), nil, nil, started)
		step.Cached = true
		return report, true, step
	}
	probeCtx, cancel := context.WithTimeout(ctx, claudeAuthProbeTimeout)
	defer cancel()
	out, err := claudeAuthCommand(probeCtx, binary, workingDir, env)
	report, ok := claudeAuthReportFromOutput(out.Stdout)
	if probeCtx.Err() != nil {
		report, ok = claudeAuthReport{}, false
	}
	if ok {
		claudeAuthReports.put(key, report)
	}
	return report, ok, authprobe.CommandStep(binary, []string{"auth", "status"}, report.status(), err, probeCtx.Err(), started)
}

func authReportCacheKey(binary, workingDir string, env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		for _, prefix := range []string{"ANTHROPIC_", "CLAUDE_", "AWS_", "GOOGLE_", "GCLOUD_", "CLOUD_ML_", "HOME"} {
			if strings.HasPrefix(key, prefix) {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	h.Write([]byte(binary))
	for _, key := range keys {
		h.Write([]byte{0})
		h.Write([]byte(key))
		h.Write([]byte{'='})
		h.Write([]byte(env[key]))
	}
	if workingDir = strings.TrimSpace(workingDir); workingDir != "" {
		for _, name := range []string{"settings.json", "settings.local.json"} {
			h.Write([]byte{0})
			if content, err := os.ReadFile(filepath.Join(workingDir, ".claude", name)); err == nil {
				h.Write(content)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

type claudeCommandOutput struct {
	Stdout []byte
	Stderr []byte
}

var claudeAuthCommand = func(ctx context.Context, binary, workingDir string, env map[string]string) (claudeCommandOutput, error) {
	cmd := aoprocess.CommandContext(ctx, binary, "auth", "status")
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = workingDir
	}
	cmd.Env = processenv.Merge(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return claudeCommandOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

func claudeAuthReportFromOutput(out []byte) (claudeAuthReport, bool) {
	start := bytes.IndexByte(out, '{')
	end := bytes.LastIndexByte(out, '}')
	if start < 0 || end < start {
		return claudeAuthReport{}, false
	}
	var report claudeAuthReport
	if json.Unmarshal(out[start:end+1], &report) != nil {
		return claudeAuthReport{}, false
	}
	if report.LoggedIn == nil {
		return claudeAuthReport{}, false
	}
	return report, true
}

func claudeLocalAuthStatus(ctx context.Context, opts agentcreds.ResolveOptions) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	for _, name := range claudeCredentialEnv {
		value := strings.TrimSpace(opts.Env(name))
		if value == "" {
			continue
		}
		method := domain.AgentAccountMethodToken
		if name == "ANTHROPIC_API_KEY" {
			method = domain.AgentAccountMethodAPIKey
		}
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(method))
		return ports.AgentAuthStatusConfigured, nil
	}
	cfgPath, err := claudeConfigPath()
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	status, err := claudeConfigAuthStatus(ctx, cfgPath)
	if status == ports.AgentAuthStatusConfigured {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(cfgPath).WithMethod(domain.AgentAccountMethodAccount))
	}
	return status, err
}

func claudeConfigAuthStatus(ctx context.Context, path string) (ports.AgentAuthStatus, error) {
	_ = ctx
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	var hasSubscription bool
	if raw := root["hasAvailableSubscription"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &hasSubscription)
	}
	var oauthAccount map[string]any
	if raw := root["oauthAccount"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &oauthAccount); err != nil {
			return ports.AgentAuthStatusUnknown, err
		}
	}
	if len(oauthAccount) == 0 {
		return ports.AgentAuthStatusUnknown, nil
	}
	if hasSubscription {
		return ports.AgentAuthStatusConfigured, nil
	}
	if accountUUID, ok := oauthAccount["accountUuid"].(string); ok && strings.TrimSpace(accountUUID) != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

var claudeModelAuthReport = func(ctx context.Context, binary, workingDir string, env map[string]string) (claudeAuthReport, bool) {
	return (&Plugin{}).claudeCLIAuthReport(ctx, binary, workingDir, env)
}

func ProviderCatalogFingerprint(ctx context.Context, binary, workingDir string, env map[string]string) string {
	probeCtx, cancel := context.WithTimeout(ctx, claudeAuthProbeTimeout)
	defer cancel()
	resolved := (&Plugin{}).resolveProviderContext(probeCtx, binary, workingDir, env, claudeModelAuthReport)
	reported := ""
	if resolved.cliOK {
		reported = strings.TrimSpace(resolved.report.APIProvider)
	}
	credential := ""
	if resolved.found {
		credential = resolved.credential.Fingerprint()
	}
	return string(resolved.provider) + "\x00" + reported + "\x00" + credential
}

func ProviderModels(ctx context.Context, binary, workingDir string, env map[string]string) ([]ports.AgentModelInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, agentcreds.DefaultTimeout)
	defer cancel()
	resolved := (&Plugin{}).resolveProviderContext(probeCtx, binary, workingDir, env, claudeModelAuthReport)
	return providerModelsFor(probeCtx, resolved)
}

type modelDiscoveryAuthError struct {
	kind    error
	message string
}

func (e modelDiscoveryAuthError) Error() string { return e.message }
func (e modelDiscoveryAuthError) Unwrap() error { return e.kind }

func providerModelsFor(probeCtx context.Context, resolved claudeProviderContext) ([]ports.AgentModelInfo, error) {
	if !resolved.providerOK {
		return nil, errors.New("configured provider is unsupported")
	}
	if resolved.found && resolved.credential.ExpiredButRenewable(claudeNow()) {
		return nil, modelDiscoveryAuthError{
			kind:    ports.ErrAgentModelDiscoveryCredentialExpired,
			message: "the saved Claude Code login token expired; Claude Code renews it the next time it runs",
		}
	}
	result := agentcreds.Result{}
	if resolved.found {
		if cached, hit := claudeAuthCache.get(resolved.credential.Fingerprint(), resolved.provider); hit &&
			(cached.State != agentcreds.StateValid || len(cached.Models) > 0) {
			result = cached
		}
	}
	if result.State == "" {
		result = claudeValidator().ValidateResolvedLocal(
			probeCtx, resolved.provider, resolved.credential, resolved.found, resolved.opts,
		)
		claudeAuthCache.put(result)
	}
	if result.State != agentcreds.StateValid && len(result.Models) == 0 {
		message := result.Detail
		if result.State == agentcreds.StateInvalid {
			return nil, modelDiscoveryAuthError{kind: ports.ErrAgentModelDiscoveryCredentialRejected, message: message}
		}
		return nil, errors.New(message)
	}
	if len(result.Models) == 0 {
		return nil, errors.New("provider reported no Claude models")
	}

	models := make([]ports.AgentModelInfo, 0, len(result.Models))
	for _, model := range result.Models {
		models = append(models, ports.AgentModelInfo{
			ID: model.ID, Label: model.DisplayName, Efforts: model.Efforts,
		})
	}
	return models, nil
}
