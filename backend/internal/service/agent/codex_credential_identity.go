package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type codexCredentialIdentity struct {
	Method            domain.CodexAuthMethod
	ProviderAccountID string
	APIKey            string
}

func parseCodexCredentialIdentity(data []byte) (codexCredentialIdentity, error) {
	var document struct {
		OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
		Tokens       *struct {
			AccountID    string `json:"account_id"`
			AccessToken  string `json:"access_token"`
			IDToken      string `json:"id_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return codexCredentialIdentity{}, errors.New("codex credential is not valid JSON")
	}
	if document.OpenAIAPIKey != nil && strings.TrimSpace(*document.OpenAIAPIKey) != "" {
		return codexCredentialIdentity{Method: domain.CodexAuthMethodAPIKey, APIKey: *document.OpenAIAPIKey}, nil
	}
	if document.Tokens == nil || (strings.TrimSpace(document.Tokens.AccessToken) == "" && strings.TrimSpace(document.Tokens.IDToken) == "" && strings.TrimSpace(document.Tokens.RefreshToken) == "") {
		return codexCredentialIdentity{}, errors.New("codex credential does not contain a supported login")
	}
	accountID := strings.TrimSpace(document.Tokens.AccountID)
	if accountID != "" && !safeProviderAccountID(accountID) {
		return codexCredentialIdentity{}, errors.New("codex credential contains an invalid account identity")
	}
	return codexCredentialIdentity{Method: domain.CodexAuthMethodChatGPT, ProviderAccountID: accountID}, nil
}

func inspectCodexCredentialIdentity(data []byte) (codexCredentialIdentity, bool) {
	identity, err := parseCodexCredentialIdentity(data)
	return identity, err == nil
}

func localCredentialIdentifiesRecord(record codexAccountRecord, data []byte) bool {
	identity, err := parseCodexCredentialIdentity(data)
	if err != nil {
		return false
	}
	if record.ProviderAccountID != "" {
		return identity.Method == domain.CodexAuthMethodChatGPT && identity.ProviderAccountID == record.ProviderAccountID
	}
	if record.Snapshot.AuthMethod != domain.CodexAuthMethodUnknown && record.Snapshot.AuthMethod != identity.Method {
		return false
	}
	saved, savedErr := readOpaqueCredential(filepath.Join(record.Home, codexCredentialFilename))
	return savedErr == nil && bytes.Equal(saved, data)
}

func safeProviderAccountID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 512 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
