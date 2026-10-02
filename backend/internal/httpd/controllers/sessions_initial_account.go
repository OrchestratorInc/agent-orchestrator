package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// SpawnAccountChoice selects one connection without changing saved defaults.
type SpawnAccountChoice struct {
	Mode      domain.AccountsManagerConnectionMode `json:"mode" enum:"native,managed"`
	AccountID string                               `json:"accountId,omitempty" maxLength:"256"`
}

func (a *SpawnAccountChoice) choice() *domain.AccountsManagerAccountChoice {
	if a == nil {
		return nil
	}
	return &domain.AccountsManagerAccountChoice{Mode: a.Mode, AccountID: a.AccountID}
}

// UnmarshalJSON keeps legacy fields compatible while rejecting ambiguous account intent.
func (r *SpawnSessionRequest) UnmarshalJSON(data []byte) error {
	if err := validateInitialAccountRequest(data); err != nil {
		return err
	}
	type wire SpawnSessionRequest
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = SpawnSessionRequest(decoded)
	return nil
}

// UnmarshalJSON applies the same account-intent boundary as standalone creation.
func (r *DelegateTaskRequest) UnmarshalJSON(data []byte) error {
	if err := validateInitialAccountRequest(data); err != nil {
		return err
	}
	type wire DelegateTaskRequest
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = DelegateTaskRequest(decoded)
	return nil
}

func validateInitialAccountRequest(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return domain.ErrAccountsManagerSelectionInvalid
	}
	seen := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return domain.ErrAccountsManagerSelectionInvalid
		}
		if !strings.EqualFold(name, "account") {
			continue
		}
		if name != "account" || seen {
			return domain.ErrAccountsManagerSelectionInvalid
		}
		seen = true
		if err := validateSpawnAccountMembers(raw); err != nil {
			return err
		}
		var choice SpawnAccountChoice
		if err := json.Unmarshal(raw, &choice); err != nil {
			return err
		}
		if !choice.choice().Valid() {
			return domain.ErrAccountsManagerSelectionInvalid
		}
	}
	return nil
}

func validateSpawnAccountMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return domain.ErrAccountsManagerSelectionInvalid
	}
	seen := make(map[string]bool, 2)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || (name != "mode" && name != "accountId") || seen[name] {
			return domain.ErrAccountsManagerSelectionInvalid
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return domain.ErrAccountsManagerSelectionInvalid
		}
	}
	if !seen["mode"] {
		return domain.ErrAccountsManagerSelectionInvalid
	}
	return nil
}

// InitialAccountSelectionResponse prevents explicit intent reaching an older decoder.
type InitialAccountSelectionResponse struct {
	InitialSelection bool `json:"initialSelection"`
}

func (c *SessionsController) accountSelection(w http.ResponseWriter, _ *http.Request) {
	capability, ok := c.Svc.(interface{ InitialAccountSelectionAvailable() bool })
	envelope.WriteJSON(w, http.StatusOK, InitialAccountSelectionResponse{InitialSelection: ok && capability.InitialAccountSelectionAvailable()})
}
