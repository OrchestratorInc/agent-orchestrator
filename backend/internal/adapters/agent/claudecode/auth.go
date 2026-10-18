package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// The auth ladder.
//
// Every rung answers only what it can prove, and a rung that cannot answer
// hands down rather than guessing. The ladder always terminates in a verdict
// that never blocks a launch.
//
//	rung 0  binary        ResolveBinary        → unknown when not installed
//	rung 1  provider gate apiProvider          → stop unless we can validate it
//	rung 2  network probe GET model list       → authorized or unauthorized
//	rung 3  cli probe     claude auth status   → unauthorized, or configured
//	rung 4  local         ~/.claude.json + env → configured, or unauthorized
//
// Rung 2 is the only rung that can prove a credential works, and so the only
// one permitted to return Authorized. Everything below it reports what is
// configured, which is a different question: presence of a credential was once
// reported as proof of authentication, so a revoked ANTHROPIC_API_KEY in a
// shell profile rendered as "ready" while every turn returned 401. Validity is
// a server-side fact — a credential can be revoked, downgraded, or rate-limited
// with no change on disk — so local evidence yields
// ports.AgentAuthStatusConfigured, which renders neutral, and never Authorized.
//
// The ladder is strictly additive at every step. A rung that cannot answer
// hands down rather than guessing, and the whole thing terminates in a verdict
// that never blocks a launch.
const claudeAuthProbeTimeout = 3 * time.Second

// claudeCredentialEnv is the environment-variable ladder in the precedence
// Claude Code itself applies.
var claudeCredentialEnv = []string{
	"CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
}

// AuthStatus reports Claude Code's authentication state without starting a
// session.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	return p.authStatusFor(ctx, "", nil, false)
}

// credentialEvidence names where the credential the agent uses came from and
// how it signs in. A token or key supplied directly carries no account
// profile, so its method replaces the CLI's "claude.ai" report.
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

// ValidateLaunchAuth checks the credential in the exact cwd/environment that
// will be handed to Claude. Launch validation always bypasses the success cache:
// a provider can revoke a credential without changing anything on disk.
func (p *Plugin) ValidateLaunchAuth(ctx context.Context, workingDir string, env map[string]string) (ports.AgentAuthStatus, error) {
	return p.authStatusFor(ctx, workingDir, env, true)
}

func (p *Plugin) authStatusFor(ctx context.Context, workingDir string, env map[string]string, fresh bool) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.authStatusAndDiagnostics(ctx, workingDir, env, fresh)
	return status, err
}

// AuthStatusWithDiagnostics reports the same verdict as AuthStatus plus the
// account `claude auth status` names, when the CLI answered for the provider
// the agent actually uses, and each check without running an extra probe.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	status, report, steps, err := p.authStatusAndDiagnostics(ctx, "", nil, false)
	return status, report.account(), steps, err
}

// authStatusAndDiagnostics runs the ladder and also returns the CLI report
// when it describes the credential the agent uses; otherwise the report is
// empty.
func (p *Plugin) authStatusAndDiagnostics(ctx context.Context, workingDir string, env map[string]string, fresh bool) (ports.AgentAuthStatus, claudeAuthReport, []domain.AgentAuthDiagnostic, error) {
	// Rung 0 — installed? A missing binary is not an auth failure; saying so
	// would point the user at a login when they need an install.
	binary, err := p.claudeBinary(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return ports.AgentAuthStatusUnknown, claudeAuthReport{}, nil, nil
		}
		return ports.AgentAuthStatusUnknown, claudeAuthReport{}, nil, err
	}

	// Rung 3 runs first among the evidence rungs, even though rung 2 outranks
	// it, because its output is what rung 2 needs: apiProvider decides whether
	// a first-party probe is even the right thing to send. Inferring the
	// provider from environment variables instead would risk pointing a
	// Bedrock credential at api.anthropic.com.
	//
	var steps []domain.AgentAuthDiagnostic
	resolved := p.resolveProviderContext(ctx, binary, workingDir, env, func(ctx context.Context, binary, cwd string, env map[string]string) (claudeAuthReport, bool) {
		report, ok, step := p.claudeCLIAuthReportDiagnostic(ctx, binary, cwd, env)
		steps = append(steps, step)
		return report, ok
	})
	reportedProvider, _ := agentcreds.ParseProvider(resolved.report.APIProvider)
	// The CLI report speaks for the agent's credential only when it answered
	// for the same provider.
	reportApplies := resolved.cliOK && (resolved.provider == agentcreds.ProviderFirstParty || resolved.provider == reportedProvider)
	report := claudeAuthReport{}
	if reportApplies {
		report = resolved.report
	}

	if resolved.found {
		// The credential the verdict is about; a token or key overrides the
		// CLI's account wording, a stored login keeps its email and plan.
		evidence := credentialEvidence(resolved.credential)
		if evidence.Method != domain.AgentAccountMethodAccount {
			report.AuthMethod = ""
		}
		ports.RecordAuthEvidence(ctx, evidence)
	} else if reportApplies {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "auth", "status"))
	}

	// Rungs 1 and 2 — the provider gate and the network probe. This is the
	// only rung that can prove a credential works, so it is the only one
	// allowed to return Authorized.
	status, ok, step := p.probeResolvedAuthDiagnostics(ctx, resolved, fresh)
	steps = append(steps, step)
	if ok {
		return status, report, steps, nil
	}

	// Rung 3's own verdict. It cannot prove success, but it is the only local
	// check that can observe a definite "signed out".
	// A first-party account report cannot reject a configured gateway or cloud
	// provider when its own probe was inconclusive.
	if reportApplies {
		status := resolved.report.status()
		if status != ports.AgentAuthStatusUnknown {
			return status, report, steps, nil
		}
	}

	// Rung 4 — local heuristic. Lowest confidence, and the last word only
	// because it never blocks anything.
	// A configured local credential is reported once, from the evidence it
	// records.
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

// resolveProviderContext is the single discovery path shared by auth checks
// and model enumeration. Keeping settings, CLI provider hints, and credential
// lookup together prevents the two callers from validating different launch
// contexts.
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

// probeAuthStatus runs the provider gate and the network probe.
//
// ok=false means the ladder must fall through: the provider is not one this
// build validates, no credential could be resolved, or the probe could not
// reach a conclusion. Only a definite provider answer stops the ladder here,
// which is what keeps the whole check additive — it can convert an Unknown
// into a real verdict, and can never manufacture a worse one.
func (p *Plugin) probeResolvedAuthStatus(ctx context.Context, resolved claudeProviderContext, fresh bool) (ports.AgentAuthStatus, bool) {
	status, ok, _ := p.probeResolvedAuthDiagnostics(ctx, resolved, fresh)
	return status, ok
}

func (p *Plugin) probeResolvedAuthDiagnostics(ctx context.Context, resolved claudeProviderContext, fresh bool) (ports.AgentAuthStatus, bool, domain.AgentAuthDiagnostic) {
	diagnostic := func(code string) domain.AgentAuthDiagnostic {
		d := domain.NewAgentAuthDiagnostic("provider_validation", code)
		d.Provider = string(resolved.provider)
		// Source labels are owned by the credential resolver, never credential values.
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

// probeAuthStatus keeps focused tests at the provider-validation boundary.
// Production callers resolve the full launch context through
// resolveProviderContext before reaching the same implementation.

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

// authStatusFromResult projects a provider verdict onto AO's vocabulary. Only
// this function may produce a verified verdict.
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

// claudeValidator builds the credential validator. It is a variable so tests
// can point the probe at a local server: a unit test must never be able to
// reach api.anthropic.com, both because that makes it flaky and because a test
// machine's real credentials are not the test's business.
var claudeValidator = func() *agentcreds.Validator { return agentcreds.New(nil) }

// authCache returns the process-wide verdict cache. It is package-level
// because the verdict is about the machine's credentials, not about any one
// plugin instance, and the readiness coordinator builds fresh adapters.
func (p *Plugin) authCache() *authCache { return claudeAuthCache }

var claudeAuthCache = newAuthCache(defaultAuthCacheTTL)

// claudeNow is the clock used to compare stored token expiry; tests pin it.
var claudeNow = time.Now

// InvalidateAuthCache drops the cached verdict. The runtime 401 handler calls
// it: the provider has just contradicted whatever was stored.
func InvalidateAuthCache() { claudeAuthCache.invalidate() }

// claudeAuthReport is the parsed shape of `claude auth status --json`. Only
// LoggedIn drives the verdict; the rest is diagnostics.
type claudeAuthReport struct {
	LoggedIn         *bool  `json:"loggedIn"`
	APIProvider      string `json:"apiProvider"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

// account is the display identity of a signed-in report. A signed-out or
// unparsed report names no account.
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

// verdict maps a CLI report onto a verdict. loggedIn:true is credentials
// present, not credentials valid — hence configured, never authorized.
func (r claudeAuthReport) status() ports.AgentAuthStatus {
	if r.LoggedIn == nil {
		return ports.AgentAuthStatusUnknown
	}
	if *r.LoggedIn {
		return ports.AgentAuthStatusConfigured
	}
	// A CLI that positively reports signed out is evidence, not a guess: it
	// consulted the same credential sources the agent will use.
	return ports.AgentAuthStatusUnauthorized
}

// claudeCLIAuthReport runs the CLI probe under a hard timeout. ok=false means
// the probe could not answer — a timeout, an exec failure, or a version whose
// output this build cannot parse — and the caller falls to the next rung.
func (p *Plugin) claudeCLIAuthReport(ctx context.Context, binary, workingDir string, env map[string]string) (claudeAuthReport, bool) {
	report, ok, _ := p.claudeCLIAuthReportDiagnostic(ctx, binary, workingDir, env)
	return report, ok
}

func (p *Plugin) claudeCLIAuthReportDiagnostic(ctx context.Context, binary, workingDir string, env map[string]string) (claudeAuthReport, bool, domain.AgentAuthDiagnostic) {
	probeCtx, cancel := context.WithTimeout(ctx, claudeAuthProbeTimeout)
	defer cancel()
	started := time.Now()
	out, err := claudeAuthCommand(probeCtx, binary, workingDir, env)
	report, ok := claudeAuthReportFromOutput(out.Stdout)
	if probeCtx.Err() != nil {
		report, ok = claudeAuthReport{}, false
	}
	return report, ok, authprobe.CommandStep(binary, []string{"auth", "status"}, report.status(), err, probeCtx.Err(), started)
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

// claudeAuthReportFromOutput extracts the JSON object the CLI prints, which may
// be surrounded by human-readable lines.
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

// claudeLocalAuthStatus is rung 4: environment variables, then ~/.claude.json.
// It reports what is configured. It can never report authorized.
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

// claudeConfigAuthStatus reads the durable markers Claude Code writes into
// ~/.claude.json. Bare userID is install/analytics identity and can survive
// logout or precede login, so only OAuth account markers count as configured.
// No durable marker proves that a credential remains authorized.
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

// ProviderCatalogFingerprint returns the local identity inputs that scope a
// Claude provider catalog. It includes the CLI-reported provider and the
// resolved credential identity without exposing the credential itself.
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

// ProviderModels returns the Claude model IDs the configured provider actually
// serves, in that provider's own ID format.
//
// It reuses the credential probe rather than adding a second network call: the
// response that proves a credential works is the same response that lists what
// that credential may use. Those two facts are inseparable — an account's model
// list is scoped to its entitlement — so discovering them together is both
// cheaper and more correct than asking twice.
//
// An error means the provider could not be asked. Callers must fall back to
// their static list rather than presenting an empty picker.
// providerProbeTimeout bounds the whole discovery probe: credential
// resolution plus the provider's paginated model listing. It is deliberately
// larger than agentcreds.DefaultTimeout, which calibrates a single validation
// probe against api.anthropic.com (~350 ms round-trip); a real gateway
// listing under that budget died with "context deadline exceeded" and the
// picker rendered the raw error (observed 2026-10-14 on z.ai). Var so tests
// can shorten it.
var providerProbeTimeout = 12 * time.Second

func ProviderModels(ctx context.Context, binary, workingDir string, env map[string]string) ([]ports.AgentModelInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, providerProbeTimeout)
	defer cancel()
	resolved := (&Plugin{}).resolveProviderContext(probeCtx, binary, workingDir, env, claudeModelAuthReport)
	return providerModelsFor(probeCtx, resolved)
}

// modelDiscoveryAuthError keeps the provider's own wording while letting the
// catalog service classify the failure with errors.Is.
type modelDiscoveryAuthError struct {
	kind    error
	message string
}

func (e modelDiscoveryAuthError) Error() string { return e.message }
func (e modelDiscoveryAuthError) Unwrap() error { return e.kind }

func providerModelsFor(probeCtx context.Context, resolved claudeProviderContext) ([]ports.AgentModelInfo, error) {
	if !resolved.providerOK {
		return nil, errors.New("claude-code: model discovery: configured provider is unsupported")
	}
	// An expired stored login cannot list models until Claude Code renews it,
	// and asking would only earn a 401 that reads like a sign-out.
	if resolved.found && resolved.credential.ExpiredButRenewable(claudeNow()) {
		return nil, modelDiscoveryAuthError{
			kind:    ports.ErrAgentModelDiscoveryCredentialExpired,
			message: "claude-code: model discovery: the saved Claude Code login token expired; Claude Code renews it the next time it runs",
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
		message := fmt.Sprintf("claude-code: model discovery: %s", result.Detail)
		if result.State == agentcreds.StateInvalid {
			return nil, modelDiscoveryAuthError{kind: ports.ErrAgentModelDiscoveryCredentialRejected, message: message}
		}
		return nil, errors.New(message)
	}
	if len(result.Models) == 0 {
		return nil, errors.New("claude-code: provider reported no Claude models")
	}

	models := make([]ports.AgentModelInfo, 0, len(result.Models))
	for _, model := range result.Models {
		models = append(models, ports.AgentModelInfo{
			ID: model.ID, Label: model.DisplayName, Efforts: model.Efforts,
		})
	}
	return models, nil
}
