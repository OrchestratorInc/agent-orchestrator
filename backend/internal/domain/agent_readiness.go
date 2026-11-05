package domain

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// AgentInstallationState is the normalized result of locating a harness CLI.
type AgentInstallationState string

const (
	// AgentInstallationInstalled means the harness binary was located.
	AgentInstallationInstalled AgentInstallationState = "installed"
	// AgentInstallationNotInstalled means the harness binary was not located.
	AgentInstallationNotInstalled AgentInstallationState = "not_installed"
	// AgentInstallationUnknown means installation could not be determined.
	AgentInstallationUnknown AgentInstallationState = "unknown"
)

// AgentAuthenticationState is the normalized result of a harness auth check.
type AgentAuthenticationState string

const (
	// AgentAuthenticationAuthorized means the harness appears signed in.
	AgentAuthenticationAuthorized AgentAuthenticationState = "authorized"
	// AgentAuthenticationUnauthorized means the harness appears signed out.
	AgentAuthenticationUnauthorized AgentAuthenticationState = "unauthorized"
	// AgentAuthenticationUnknown means authentication could not be determined.
	AgentAuthenticationUnknown AgentAuthenticationState = "unknown"
	// AgentAuthenticationNotApplicable means the harness requires no auth check.
	AgentAuthenticationNotApplicable AgentAuthenticationState = "not_applicable"
	// AgentAuthenticationConfigured means a credential is present locally but
	// nothing has proven it valid. It is deliberately not a ready state: a
	// revoked key looks identical to a working one on disk.
	AgentAuthenticationConfigured AgentAuthenticationState = "configured"
)

// AgentEffectiveReadiness is derived from installation and authentication.
type AgentEffectiveReadiness string

const (
	// AgentReadinessReady means installation and authentication allow use.
	AgentReadinessReady AgentEffectiveReadiness = "ready"
	// AgentReadinessNotReady means a definite observation blocks use.
	AgentReadinessNotReady AgentEffectiveReadiness = "not_ready"
	// AgentReadinessUnknown means readiness cannot be determined safely.
	AgentReadinessUnknown AgentEffectiveReadiness = "unknown"
)

// AgentReadinessFreshness describes whether an observation can satisfy an
// ensure request without native work.
type AgentReadinessFreshness string

const (
	// AgentReadinessFresh means an observation satisfies its freshness policy.
	AgentReadinessFresh AgentReadinessFreshness = "fresh"
	// AgentReadinessStale means an observation should be rechecked.
	AgentReadinessStale AgentReadinessFreshness = "stale"
	// AgentReadinessChecking means a shared native check is in flight.
	AgentReadinessChecking AgentReadinessFreshness = "checking"
)

// AgentReadinessPurpose selects the freshness policy used by Ensure.
type AgentReadinessPurpose string

const (
	// AgentReadinessPurposeDisplay selects the five-minute display policy.
	AgentReadinessPurposeDisplay AgentReadinessPurpose = "display"
	// AgentReadinessPurposeLaunch selects the thirty-second launch policy.
	AgentReadinessPurposeLaunch AgentReadinessPurpose = "launch"
)

// Valid reports whether the purpose selects a supported freshness policy.
func (p AgentReadinessPurpose) Valid() bool {
	return p == AgentReadinessPurposeDisplay || p == AgentReadinessPurposeLaunch
}

// Stable, safe reason codes exposed through the daemon API.
const (
	AgentReadinessReasonNotChecked              = "not_checked"
	AgentReadinessReasonChecking                = "checking"
	AgentReadinessReasonInstalled               = "installed"
	AgentReadinessReasonNotInstalled            = "not_installed"
	AgentReadinessReasonInstallIdentityPending  = "install_identity_pending"
	AgentReadinessReasonInstallCheckUnsupported = "install_check_unsupported"
	AgentReadinessReasonInstallCheckTimeout     = "install_check_timeout"
	AgentReadinessReasonInstallCheckFailed      = "install_check_failed"
	AgentReadinessReasonAuthorized              = "authorized"
	AgentReadinessReasonAuthConfigured          = "auth_configured_unverified"
	AgentReadinessReasonUnauthorized            = "unauthorized"
	AgentReadinessReasonAuthNotApplicable       = "auth_not_applicable"
	AgentReadinessReasonAuthCheckUnsupported    = "auth_check_unsupported"
	AgentReadinessReasonAuthCheckInconclusive   = "auth_check_inconclusive"
	AgentReadinessReasonAuthCheckTimeout        = "auth_check_timeout"
	AgentReadinessReasonAuthCheckFailed         = "auth_check_failed"
	AgentReadinessReasonAuthSkippedNotInstalled = "auth_skipped_not_installed"
)

// AgentInstallationObservation records the latest normalized installation check.
type AgentInstallationObservation struct {
	State       AgentInstallationState  `json:"state" enum:"installed,not_installed,unknown"`
	Freshness   AgentReadinessFreshness `json:"freshness" enum:"fresh,stale,checking"`
	CheckedAt   *time.Time              `json:"checkedAt" format:"date-time"`
	AttemptedAt *time.Time              `json:"attemptedAt" format:"date-time"`
	ReasonCode  string                  `json:"reasonCode"`
	Reason      string                  `json:"reason"`
}

// AgentAuthenticationObservation records the latest normalized authentication check.
type AgentAuthenticationObservation struct {
	State       AgentAuthenticationState `json:"state" enum:"authorized,unauthorized,unknown,not_applicable,configured"`
	Freshness   AgentReadinessFreshness  `json:"freshness" enum:"fresh,stale,checking"`
	CheckedAt   *time.Time               `json:"checkedAt" format:"date-time"`
	AttemptedAt *time.Time               `json:"attemptedAt" format:"date-time"`
	ReasonCode  string                   `json:"reasonCode"`
	Reason      string                   `json:"reason"`
	// Account names the account the check observed, when the harness reports
	// one. It is display-only and present only with authorized or configured.
	Account     *AgentAccountIdentity `json:"account,omitempty"`
	Diagnostics []AgentAuthDiagnostic `json:"diagnostics,omitempty"`
}

// AgentAccountMethod is how a harness is signed in.
type AgentAccountMethod string

const (
	// AgentAccountMethodAccount is a sign-in to the provider's account.
	AgentAccountMethodAccount AgentAccountMethod = "account"
	// AgentAccountMethodAPIKey is an API key.
	AgentAccountMethodAPIKey AgentAccountMethod = "api_key"
	// AgentAccountMethodToken is an OAuth, setup or session token supplied
	// without an account profile, such as CLAUDE_CODE_OAUTH_TOKEN.
	AgentAccountMethodToken AgentAccountMethod = "token"
)

// AgentCredentialSourceKind is where a harness's credential was found.
type AgentCredentialSourceKind string

const (
	// AgentCredentialSourceEnvironment is an environment variable.
	AgentCredentialSourceEnvironment AgentCredentialSourceKind = "environment"
	// AgentCredentialSourceFile is a credential or config file.
	AgentCredentialSourceFile AgentCredentialSourceKind = "file"
	// AgentCredentialSourceCommand is the harness's own status command.
	AgentCredentialSourceCommand AgentCredentialSourceKind = "command"
	// AgentCredentialSourceKeychain is the operating system's keychain.
	AgentCredentialSourceKeychain AgentCredentialSourceKind = "keychain"
)

// AgentAccountIdentity is what a harness reports about its signed-in account.
// Every field is optional. None of it proves the credential works, and none of
// it is provider or storage identity: no account ids, tokens or paths.
type AgentAccountIdentity struct {
	Email        string `json:"email,omitempty"`
	Name         string `json:"name,omitempty"`
	Organization string `json:"organization,omitempty"`
	Plan         string `json:"plan,omitempty"`
	// Provider is the model provider an API key or login is for, when the
	// harness supports several (e.g. "zai-coding-plan").
	Provider string             `json:"provider,omitempty"`
	Method   AgentAccountMethod `json:"method,omitempty" enum:"account,api_key,token"`
	// SourceKind and Source name the evidence the check relied on: a variable
	// name, a home-relative file path or a command. Never a credential value.
	SourceKind AgentCredentialSourceKind `json:"sourceKind,omitempty" enum:"environment,file,command,keychain"`
	Source     string                    `json:"source,omitempty"`
}

// EnvironmentSource names an environment variable as the credential source.
func EnvironmentSource(name string) AgentAccountIdentity {
	return AgentAccountIdentity{SourceKind: AgentCredentialSourceEnvironment, Source: name}
}

// CommandSource names the status command that reported the login, by the
// command's base name and arguments.
func CommandSource(binary string, args ...string) AgentAccountIdentity {
	return AgentAccountIdentity{SourceKind: AgentCredentialSourceCommand, Source: strings.Join(append([]string{filepath.Base(binary)}, args...), " ")}
}

// KeychainSource names the operating system keychain as the source.
func KeychainSource() AgentAccountIdentity {
	return AgentAccountIdentity{SourceKind: AgentCredentialSourceKeychain}
}

// WithMethod returns a copy with the sign-in method set.
func (a AgentAccountIdentity) WithMethod(method AgentAccountMethod) AgentAccountIdentity {
	a.Method = method
	return a
}

// Merge fills fields a leaves empty from other.
func (a AgentAccountIdentity) Merge(other AgentAccountIdentity) AgentAccountIdentity {
	if a.Email == "" {
		a.Email = other.Email
	}
	if a.Name == "" {
		a.Name = other.Name
	}
	if a.Organization == "" {
		a.Organization = other.Organization
	}
	if a.Plan == "" {
		a.Plan = other.Plan
	}
	if a.Provider == "" {
		a.Provider = other.Provider
	}
	if a.Method == "" {
		a.Method = other.Method
	}
	if a.SourceKind == "" {
		a.SourceKind, a.Source = other.SourceKind, other.Source
	}
	return a
}

// maxAccountIdentityRunes bounds each display field; 254 is the longest
// deliverable email address.
const maxAccountIdentityRunes = 254

// Sanitized trims each field, drops values with control characters or past
// the length bound, and returns nil when nothing displayable remains.
func (a AgentAccountIdentity) Sanitized() *AgentAccountIdentity {
	clean := AgentAccountIdentity{
		Email:        displayAccountField(a.Email),
		Name:         displayAccountField(a.Name),
		Organization: displayAccountField(a.Organization),
		Plan:         displayAccountField(a.Plan),
		Provider:     displayAccountField(a.Provider),
	}
	switch a.Method {
	case AgentAccountMethodAccount, AgentAccountMethodAPIKey, AgentAccountMethodToken:
		clean.Method = a.Method
	}
	switch a.SourceKind {
	case AgentCredentialSourceEnvironment, AgentCredentialSourceFile, AgentCredentialSourceCommand:
		if source := displayAccountField(a.Source); source != "" {
			clean.SourceKind, clean.Source = a.SourceKind, source
		}
	case AgentCredentialSourceKeychain:
		clean.SourceKind = a.SourceKind
	}
	if !strings.Contains(clean.Email, "@") {
		clean.Email = ""
	}
	if clean == (AgentAccountIdentity{}) {
		return nil
	}
	return &clean
}

func displayAccountField(value string) string {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxAccountIdentityRunes {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ""
		}
	}
	return value
}

// AgentReadinessSnapshot is the daemon-owned readiness view for one harness.
// EffectiveReadiness is always derived when a snapshot is read.
type AgentReadinessSnapshot struct {
	ID                 string                         `json:"id"`
	Label              string                         `json:"label"`
	Installation       AgentInstallationObservation   `json:"installation"`
	Authentication     AgentAuthenticationObservation `json:"authentication"`
	EffectiveReadiness AgentEffectiveReadiness        `json:"effectiveReadiness" enum:"ready,not_ready,unknown"`
	UsageCount         int                            `json:"usageCount"`
	LastUsedAt         *time.Time                     `json:"lastUsedAt,omitempty" format:"date-time"`
}

// EffectiveAgentReadiness derives readiness without treating unknown as a negative result.
func EffectiveAgentReadiness(installation AgentInstallationState, authentication AgentAuthenticationState) AgentEffectiveReadiness {
	switch installation {
	case AgentInstallationNotInstalled:
		return AgentReadinessNotReady
	case AgentInstallationUnknown:
		return AgentReadinessUnknown
	case AgentInstallationInstalled:
		switch authentication {
		case AgentAuthenticationAuthorized, AgentAuthenticationNotApplicable:
			return AgentReadinessReady
		case AgentAuthenticationUnauthorized:
			return AgentReadinessNotReady
		default:
			// Configured and unknown both land here. A credential AO could not
			// verify must not read as ready, and must not block a launch
			// either — unknown does both.
			return AgentReadinessUnknown
		}
	default:
		return AgentReadinessUnknown
	}
}
