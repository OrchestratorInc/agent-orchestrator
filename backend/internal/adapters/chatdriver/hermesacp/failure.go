package hermesacp

import (
	"encoding/json"
	"errors"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const hermesAdmittedTurnFailed = "admitted_turn_failed"

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
