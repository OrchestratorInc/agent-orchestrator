package proxyhost

import (
	"encoding/base64"
	"strings"
	"testing"
)

func jwtForTest(payload string) string {
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
}

func TestNormalizeCodexNativeAuth(t *testing.T) {
	idToken := jwtForTest(`{"email":"native@example.test"}`)
	credential, err := normalizeNative("codex", []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","id_token":"`+idToken+`"}}`))
	if err != nil || credential.Fingerprint == "" {
		t.Fatalf("normalize codex: %+v err=%v", credential, err)
	}
	if !strings.Contains(credential.CredentialJSON, `"type":"codex"`) || !strings.Contains(credential.CredentialJSON, `"refresh_token":"refresh"`) {
		t.Fatalf("normalized codex credential lost required fields: %s", credential.CredentialJSON)
	}
}

func TestNormalizeClaudeNativeAuth(t *testing.T) {
	access := jwtForTest(`{"sub":"claude@example.test"}`)
	credential, err := normalizeNative("claude", []byte(`{"claudeAiOauth":{"accessToken":"`+access+`","refreshToken":"refresh"}}`))
	if err != nil || credential.Fingerprint == "" {
		t.Fatalf("normalize claude: %+v err=%v", credential, err)
	}
	if !strings.Contains(credential.CredentialJSON, `"type":"claude"`) || !strings.Contains(credential.CredentialJSON, `"refresh_token":"refresh"`) {
		t.Fatalf("normalized claude credential lost required fields: %s", credential.CredentialJSON)
	}
}

func TestNormalizeMissingNativeAuthIsNotFound(t *testing.T) {
	if credential, err := normalizeNative("codex", []byte(`{"tokens":{}}`)); err != nil || credential.Fingerprint != "" {
		t.Fatalf("missing codex auth: %+v err=%v", credential, err)
	}
	if credential, err := normalizeNative("claude", []byte(`{"claudeAiOauth":{}}`)); err != nil || credential.Fingerprint != "" {
		t.Fatalf("missing claude auth: %+v err=%v", credential, err)
	}
}
