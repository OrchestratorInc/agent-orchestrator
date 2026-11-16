package agentaccount

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Approving a key records only its last 20 characters, as Claude Code does,
// clears an earlier refusal of it, and keeps the rest of the account's config.
func TestApproveAPIKeyRecordsTheKeyLikeClaudeCode(t *testing.T) {
	home := t.TempDir()
	key := "sk-ant-api03-" + strings.Repeat("x", 30) + "0123456789abcdefghij"
	suffix := "0123456789abcdefghij"
	config := `{"hasCompletedOnboarding":true,"customApiKeyResponses":{"approved":["older-key-suffix-0000"],"rejected":["` + suffix + `"]}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	claude := &Claude{}
	if err := claude.ApproveAPIKey(home, key); err != nil {
		t.Fatal(err)
	}
	if err := claude.ApproveAPIKey(home, key); err != nil { // again: no duplicate
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-ant-api03-") {
		t.Fatal(".claude.json holds more of the key than its last 20 characters")
	}
	var got struct {
		HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
		Responses              struct {
			Approved []string `json:"approved"`
			Rejected []string `json:"rejected"`
		} `json:"customApiKeyResponses"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.HasCompletedOnboarding {
		t.Fatal("approving a key dropped the rest of the config")
	}
	if !slices.Equal(got.Responses.Approved, []string{"older-key-suffix-0000", suffix}) || len(got.Responses.Rejected) != 0 {
		t.Fatalf("customApiKeyResponses = %+v", got.Responses)
	}
}
