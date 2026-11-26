package agent

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Diagnostics describe the evidence; they never change the authentication verdict.
func completeAuthenticationDiagnostics(observation *domain.AgentAuthenticationObservation) {
	codes := make(map[string]bool)
	for _, step := range observation.Diagnostics {
		codes[step.Code] = true
	}
	if codes["cli_signed_in"] && codes["provider_rejected"] && !codes["native_login_rejected"] {
		observation.Diagnostics = append(observation.Diagnostics, domain.NewAgentAuthDiagnostic("authentication", "native_login_rejected"))
	}
	if observation.ReasonCode == domain.AgentReadinessReasonAuthCheckTimeout && !codes["probe_timeout"] {
		observation.Diagnostics = append(observation.Diagnostics, domain.NewAgentAuthDiagnostic("authentication", "probe_timeout"))
		return
	}
	// Keep detailed adapter failures intact. Only supply a fallback when the
	// adapter did not report why the check could not establish authentication.
	for code := range codes {
		if code != "credential_file_read" && code != "credential_present" && code != "account_context" {
			return
		}
	}
	code := ""
	switch observation.ReasonCode {
	case domain.AgentReadinessReasonAuthCheckUnsupported:
		code = "check_unsupported"
	case domain.AgentReadinessReasonAuthCheckInconclusive:
		code = "check_inconclusive"
	case domain.AgentReadinessReasonAuthCheckTimeout:
		code = "probe_timeout"
	case domain.AgentReadinessReasonAuthCheckFailed:
		code = "check_failed"
	}
	if code != "" {
		observation.Diagnostics = append(observation.Diagnostics, domain.NewAgentAuthDiagnostic("authentication", code))
	}
}
