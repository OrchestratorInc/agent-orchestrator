package browser

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type Authority struct{}

func NewAuthority() *Authority { return &Authority{} }

func (a *Authority) Issue(sessionID domain.SessionID) (token, verifier string, err error) {
	if a == nil || sessionID == "" {
		return "", "", fmt.Errorf("issue browser capability: session id is required")
	}
	raw := make([]byte, sha256.Size)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate browser capability: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, capabilityVerifier(sessionID, token), nil
}

func (a *Authority) Valid(sessionID domain.SessionID, token, verifier string) bool {
	if a == nil || sessionID == "" || token == "" || verifier == "" {
		return false
	}
	expected := capabilityVerifier(sessionID, token)
	return hmac.Equal([]byte(expected), []byte(verifier))
}

func capabilityVerifier(sessionID domain.SessionID, token string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("ao-browser-capability-verifier-v1\x00"))
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
