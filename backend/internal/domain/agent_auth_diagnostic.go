package domain

import "time"

// AgentAuthDiagnostic is evidence from one auth check, not a new auth verdict.
// Producers supply only fixed method/source labels and classified outcomes;
// never include credentials, CLI output, provider bodies, URLs or raw errors.
type AgentAuthDiagnostic struct {
	Method     string    `json:"method"`
	Code       string    `json:"code"`
	Reason     string    `json:"reason"`
	NextStep   string    `json:"nextStep,omitempty"`
	Source     string    `json:"source,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	CheckedAt  time.Time `json:"checkedAt" format:"date-time"`
	Cached     bool      `json:"cached"`
	DurationMs int64     `json:"durationMs"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	HTTPStatus int       `json:"httpStatus,omitempty"`
}

// NewAgentAuthDiagnostic uses authored explanations rather than exporting raw
// subprocess or provider messages, which can echo tokens even on failure.
func NewAgentAuthDiagnostic(method, code string) AgentAuthDiagnostic {
	d := AgentAuthDiagnostic{Method: method, Code: code, CheckedAt: time.Now()}
	switch code {
	case "probe_timeout":
		d.Reason = "The authentication check timed out; this does not prove you are signed out."
		d.NextStep = "Check the CLI and network connection, then check again."
	case "probe_cancelled":
		d.Reason = "The authentication check was cancelled before it finished."
		d.NextStep = "Check again."
	case "command_failed":
		d.Reason = "The CLI authentication command failed without a recognizable authentication result."
		d.NextStep = "Run the indicated command on the selected host and check its installation and runtime dependencies."
	case "output_unrecognized":
		d.Reason = "AO could not recognize the CLI authentication response."
		d.NextStep = "Check the CLI version and its native login status; include these diagnostics in a bug report."
	case "cli_signed_out":
		d.Reason = "The CLI explicitly reported that it is signed out."
		d.NextStep = "Log in to this harness on the selected host, then check again."
	case "cli_signed_in":
		d.Reason = "The CLI reported a stored login; this check alone does not prove provider access."
	case "credential_present":
		d.Reason = "A local credential is configured; its presence alone does not prove provider access."
	case "credential_missing":
		d.Reason = "AO could not resolve a credential for this provider."
		d.NextStep = "Check the harness login and credential environment on the selected host."
	case "provider_unsupported":
		d.Reason = "AO cannot directly verify the selected provider configuration."
		d.NextStep = "Verify access using the harness on the selected host."
	case "token_renewal_pending":
		d.Reason = "The stored access token has expired, but a refresh token is available. AO leaves renewal to the harness."
		d.NextStep = "Run the harness so it can renew its login, then check again."
	case "provider_accepted":
		d.Reason = "The provider accepted the selected credential."
	case "provider_rejected":
		d.Reason = "The provider rejected the selected credential."
		d.NextStep = "Check the credential source below. An environment credential can override a stored login; update that source or log in again."
	case "provider_unreachable":
		d.Reason = "The provider check could not complete because of a connection or transport error."
		d.NextStep = "Check network, proxy and TLS settings on the selected host, then check again."
	default:
		d.Reason = "The provider response did not establish whether the credential works."
		d.NextStep = "Check provider availability and account access, then check again."
	}
	return d
}
