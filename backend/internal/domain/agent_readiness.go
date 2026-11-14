package domain

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AgentInstallationState string

const (
	AgentInstallationInstalled    AgentInstallationState = "installed"
	AgentInstallationNotInstalled AgentInstallationState = "not_installed"
	AgentInstallationUnknown      AgentInstallationState = "unknown"
)

type AgentAuthenticationState string

const (
	AgentAuthenticationAuthorized    AgentAuthenticationState = "authorized"
	AgentAuthenticationUnauthorized  AgentAuthenticationState = "unauthorized"
	AgentAuthenticationUnknown       AgentAuthenticationState = "unknown"
	AgentAuthenticationNotApplicable AgentAuthenticationState = "not_applicable"
	AgentAuthenticationConfigured    AgentAuthenticationState = "configured"
)

type AgentEffectiveReadiness string

const (
	AgentReadinessReady    AgentEffectiveReadiness = "ready"
	AgentReadinessNotReady AgentEffectiveReadiness = "not_ready"
	AgentReadinessUnknown  AgentEffectiveReadiness = "unknown"
)

type AgentReadinessFreshness string

const (
	AgentReadinessFresh    AgentReadinessFreshness = "fresh"
	AgentReadinessStale    AgentReadinessFreshness = "stale"
	AgentReadinessChecking AgentReadinessFreshness = "checking"
)

type AgentReadinessPurpose string

const (
	AgentReadinessPurposeDisplay AgentReadinessPurpose = "display"
	AgentReadinessPurposeLaunch  AgentReadinessPurpose = "launch"
)

func (p AgentReadinessPurpose) Valid() bool {
	return p == AgentReadinessPurposeDisplay || p == AgentReadinessPurposeLaunch
}

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

type AgentInstallationObservation struct {
	State       AgentInstallationState  `json:"state" enum:"installed,not_installed,unknown"`
	Freshness   AgentReadinessFreshness `json:"freshness" enum:"fresh,stale,checking"`
	CheckedAt   *time.Time              `json:"checkedAt" format:"date-time"`
	AttemptedAt *time.Time              `json:"attemptedAt" format:"date-time"`
	ReasonCode  string                  `json:"reasonCode"`
	Reason      string                  `json:"reason"`
}

type AgentAuthenticationObservation struct {
	State       AgentAuthenticationState `json:"state" enum:"authorized,unauthorized,unknown,not_applicable,configured"`
	Freshness   AgentReadinessFreshness  `json:"freshness" enum:"fresh,stale,checking"`
	CheckedAt   *time.Time               `json:"checkedAt" format:"date-time"`
	AttemptedAt *time.Time               `json:"attemptedAt" format:"date-time"`
	ReasonCode  string                   `json:"reasonCode"`
	Reason      string                   `json:"reason"`
	Account     *AgentAccountIdentity    `json:"account,omitempty"`
	Diagnostics []AgentAuthDiagnostic    `json:"diagnostics,omitempty"`
}

type AgentAccountMethod string

const (
	AgentAccountMethodAccount AgentAccountMethod = "account"
	AgentAccountMethodAPIKey  AgentAccountMethod = "api_key"
	AgentAccountMethodToken   AgentAccountMethod = "token"
)

type AgentCredentialSourceKind string

const (
	AgentCredentialSourceEnvironment AgentCredentialSourceKind = "environment"
	AgentCredentialSourceFile        AgentCredentialSourceKind = "file"
	AgentCredentialSourceCommand     AgentCredentialSourceKind = "command"
	AgentCredentialSourceKeychain    AgentCredentialSourceKind = "keychain"
)

type AgentAccountIdentity struct {
	Email        string                    `json:"email,omitempty"`
	Name         string                    `json:"name,omitempty"`
	Organization string                    `json:"organization,omitempty"`
	Plan         string                    `json:"plan,omitempty"`
	Provider     string                    `json:"provider,omitempty"`
	Method       AgentAccountMethod        `json:"method,omitempty" enum:"account,api_key,token"`
	SourceKind   AgentCredentialSourceKind `json:"sourceKind,omitempty" enum:"environment,file,command,keychain"`
	Source       string                    `json:"source,omitempty"`
}

func EnvironmentSource(name string) AgentAccountIdentity {
	return AgentAccountIdentity{SourceKind: AgentCredentialSourceEnvironment, Source: name}
}

func CommandSource(binary string, args ...string) AgentAccountIdentity {
	return AgentAccountIdentity{SourceKind: AgentCredentialSourceCommand, Source: strings.Join(append([]string{filepath.Base(binary)}, args...), " ")}
}

func KeychainSource() AgentAccountIdentity {
	return AgentAccountIdentity{SourceKind: AgentCredentialSourceKeychain}
}

func (a AgentAccountIdentity) WithMethod(method AgentAccountMethod) AgentAccountIdentity {
	a.Method = method
	return a
}

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

const maxAccountIdentityRunes = 254

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

type AgentReadinessSnapshot struct {
	ID                 string                         `json:"id"`
	Label              string                         `json:"label"`
	Installation       AgentInstallationObservation   `json:"installation"`
	Authentication     AgentAuthenticationObservation `json:"authentication"`
	EffectiveReadiness AgentEffectiveReadiness        `json:"effectiveReadiness" enum:"ready,not_ready,unknown"`
	UsageCount         int                            `json:"usageCount"`
	LastUsedAt         *time.Time                     `json:"lastUsedAt,omitempty" format:"date-time"`
}

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
			return AgentReadinessUnknown
		}
	default:
		return AgentReadinessUnknown
	}
}
