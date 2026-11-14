package hermesacp

import (
	"encoding/json"
	"errors"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// hermesAdmittedTurnFailed is the session/prompt error detail Hermes' gateway
// returns for a turn it accepted that then failed (acp_adapter/gateway_server.py
// in Hermes v0.22.5).
const hermesAdmittedTurnFailed = "admitted_turn_failed"

// promptErrorFailure restates Hermes' admitted_turn_failed answer. Hermes
// streams its own explanation of the failure (the provider's billing, rate
// limit or refusal copy) as the turn's reply, then answers session/prompt with
// a bare JSON-RPC internal error that names only this code. That error is not
// something to show the user, so the turn keeps a short summary instead.
func promptErrorFailure(err error, answered bool) error {
	if promptErrorDetail(err) != hermesAdmittedTurnFailed {
		return nil
	}
	message := "Hermes could not finish this turn."
	if answered {
		message += " Its reply above says why."
	}
	return ports.NewChatProviderFailure(message, "", err)
}

// promptErrorDetail reads the details code of a Hermes internal error. AO's
// persistent chat host wraps the agent's error data under providerData.
func promptErrorDetail(err error) string {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 || requestErr.Data == nil {
		return ""
	}
	raw, err := json.Marshal(requestErr.Data)
	if err != nil {
		return ""
	}
	var data struct {
		Details      string `json:"details"`
		ProviderData *struct {
			Details string `json:"details"`
		} `json:"providerData"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return ""
	}
	if data.ProviderData != nil {
		return data.ProviderData.Details
	}
	return data.Details
}
