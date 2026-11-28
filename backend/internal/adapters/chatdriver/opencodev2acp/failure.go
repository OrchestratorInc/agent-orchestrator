package opencodev2acp

import (
	"encoding/json"
	"errors"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// providerTransport is the errorName OpenCode 2 sends when it cannot connect
// to the model server, for example "FailedToOpenSocket: Was there a typo in the url or port?".
const providerTransport = "provider.transport"

// promptErrorFailure restates a provider.transport answer, which otherwise
// reaches the user as raw JSON-RPC. OpenCode's own message stays as the detail.
func promptErrorFailure(err error, _ bool) error {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 || providerErrorName(requestErr.Data) != providerTransport {
		return nil
	}
	return ports.NewChatProviderFailure(
		"OpenCode could not connect to the model server. Check that the server is running and that the URL in your OpenCode provider config is reachable from this computer.",
		strings.TrimPrefix(requestErr.Message, "Internal error: "),
		err,
	)
}

// providerErrorName reads OpenCode's errorName. AO's persistent chat host
// wraps the agent's error data under providerData.
func providerErrorName(data any) string {
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var fields struct {
		ErrorName    string `json:"errorName"`
		ProviderData *struct {
			ErrorName string `json:"errorName"`
		} `json:"providerData"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	if fields.ProviderData != nil {
		return fields.ProviderData.ErrorName
	}
	return fields.ErrorName
}
