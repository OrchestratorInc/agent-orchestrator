package domain

import "time"

// AgentAuthDiagnostic is evidence from one auth check, not a new auth verdict.
// Producers supply only authored methods, adapter-owned source paths/labels and classified outcomes;
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
	case "credential_file_missing":
		d.Reason = "Orchestrator did not find the credential file at the location this harness checks."
		d.NextStep = "Compare the selected host, user and harness config directory with the terminal where login works. A terminal-only config override may not be present in Orchestrator."
	case "credential_file_empty":
		d.Reason = "The credential file Orchestrator read is empty."
		d.NextStep = "Check that the working terminal uses this same credential store, then use the harness's login flow to repair it."
	case "credential_file_read":
		d.Reason = "Orchestrator could read this credential file; this alone does not establish a usable login."
	case "credential_file_invalid":
		d.Reason = "Orchestrator could not parse the credential file in the format this adapter expects."
		d.NextStep = "Check the harness version and credential-store format. If native login works, include these diagnostics in a bug report; do not share the file contents."
	case "credential_store_unreadable":
		d.Reason = "Orchestrator could not read the credential store."
		d.NextStep = "Check file permissions, keychain access, and the user running Orchestrator on the selected host."
	case "credential_reference_unresolved":
		d.Reason = "The configured key refers to an environment variable that is missing or empty in Orchestrator's process."
		d.NextStep = "Make that variable available to the Orchestrator process, then restart Orchestrator and check again. A variable set only in a terminal shell may not reach the desktop app."
	case "credential_command_deferred":
		d.Reason = "The credential is supplied by a command. Orchestrator does not execute credential commands during this background check."
		d.NextStep = "Confirm access in the harness itself. An inconclusive Orchestrator check here does not mean the native login is broken."
	case "credential_environment_missing":
		d.Reason = "None of the credential variables checked here is set in Orchestrator's process."
		d.NextStep = "Compare these variable names with the working terminal. If you change Orchestrator's environment, restart Orchestrator and check again; do not copy key values into a report."
	case "credentials_disabled":
		d.Reason = "The native credential store contains records, but none has an enabled, nonempty credential."
		d.NextStep = "Review the provider's login in the harness; disabled or empty records do not establish a usable login."
	case "check_unsupported":
		d.Reason = "Orchestrator does not have a supported authentication check for this harness."
		d.NextStep = "Use the harness's native login check on the selected host. Orchestrator cannot infer that you are signed out."
	case "check_inconclusive":
		d.Reason = "Orchestrator could not determine authentication from the available evidence. This is not proof that the harness is signed out."
		d.NextStep = "Compare the executable and credential sources below with the working terminal, then re-check. If they match, report the harness version and these diagnostics."
	case "check_failed":
		d.Reason = "Orchestrator's authentication check failed before it could establish a result."
		d.NextStep = "Review the preceding check outcomes and compare the executable, host and credential source with the working terminal."
	case "native_login_rejected":
		d.Reason = "The native CLI reported a stored login, but the provider rejected the credential selected for Orchestrator's check."
		d.NextStep = "Compare the selected provider and credential source below with the working terminal. A different or expired environment credential can take precedence over a stored login."
	case "account_sync_pending":
		d.Reason = "Orchestrator has not reconciled the device's Codex login with its account store yet. This does not prove the local CLI is signed out."
		d.NextStep = "Refresh Codex accounts in Settings and check again. Confirm that the account selected in Orchestrator matches the native CLI account."
	case "account_store_unavailable":
		d.Reason = "Orchestrator's Codex account store could not answer; Orchestrator is falling back to the native authentication check."
		d.NextStep = "Review the native check below and refresh Codex accounts in Settings."
	case "account_context":
		d.Reason = "This result comes from the Codex account selected in Orchestrator, which may differ from the account in a separate terminal."
		d.NextStep = "Compare the selected Orchestrator account with the native CLI account before signing in again."
	case "saved_default_account":
		d.Reason = "New sessions start on a saved account, so this result is that account's sign-in, not the CLI's own login."
		d.NextStep = "If the account is signed out, sign it in again on the harness's Account tab, or make another account the default."
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
		d.Reason = "Orchestrator could not recognize the CLI authentication response."
		d.NextStep = "Check the CLI version and its native login status; include these diagnostics in a bug report."
	case "cli_signed_out":
		d.Reason = "The CLI explicitly reported that it is signed out."
		d.NextStep = "Run the same executable and status command on the selected host. If it reports a login in your terminal, compare the user, config directory and environment seen by Orchestrator before signing in again."
	case "cli_signed_in":
		d.Reason = "The CLI reported a stored login; this check alone does not prove provider access."
	case "credential_present":
		d.Reason = "A local credential is configured; its presence alone does not prove provider access."
	case "credential_missing":
		d.Reason = "Orchestrator could not resolve a credential for this provider."
		d.NextStep = "Check the harness login and credential environment on the selected host."
	case "provider_unsupported":
		d.Reason = "Orchestrator cannot directly verify the selected provider configuration."
		d.NextStep = "Verify access using the harness on the selected host."
	case "model_selection_unresolved":
		d.Reason = "Orchestrator could not resolve the configured or recent model from the harness's available models."
		d.NextStep = "Select an available model in the harness, then check again."
	case "token_renewal_pending":
		d.Reason = "Signed in. The stored access token has expired; the harness renews it with its refresh token the next time it runs, so no action is needed."
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
