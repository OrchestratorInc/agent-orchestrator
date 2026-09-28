package runner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"maps"
	"strings"
	"time"
)

type routeBinding struct {
	Blocked   bool   `json:"blocked"`
	SessionID string `json:"sessionId"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	AccountID string `json:"accountId"`
	Revision  int64  `json:"revision"`
}

type routeBindingSnapshot struct {
	Revision int64          `json:"revision"`
	Bindings []routeBinding `json:"bindings"`
}

func bindingKey(sessionID, provider string) string { return sessionID + "\x00" + provider }

func validRouteAtom(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func (c *routeCapability) Reconcile(snapshot routeBindingSnapshot) error {
	if snapshot.Revision <= 0 || len(snapshot.Bindings) > 10000 {
		return errPinnedAccountUnavailable
	}
	bindings := make(map[string]routeBinding, len(snapshot.Bindings))
	for _, binding := range snapshot.Bindings {
		if !validRouteAtom(binding.SessionID, 256) || !validVaultProvider(binding.Provider) || binding.Revision <= 0 || binding.Revision > snapshot.Revision ||
			(binding.Mode != "native" || binding.AccountID != "") && (binding.Mode != "managed" || !validRouteAtom(binding.AccountID, 128)) {
			return errPinnedAccountUnavailable
		}
		key := bindingKey(binding.SessionID, binding.Provider)
		if _, exists := bindings[key]; exists {
			return errPinnedAccountUnavailable
		}
		bindings[key] = binding
	}
	c.bindingMu.Lock()
	defer c.bindingMu.Unlock()
	if snapshot.Revision < c.bindingRevision || (snapshot.Revision == c.bindingRevision && !maps.Equal(bindings, c.bindings)) {
		return errPinnedAccountUnavailable
	}
	for key, previous := range c.bindings {
		next, exists := bindings[key]
		previous.Blocked = next.Blocked
		if exists && (next.Revision < previous.Revision || (next.Revision == previous.Revision && next != previous)) {
			return errPinnedAccountUnavailable
		}
	}
	c.bindingRevision, c.bindings = snapshot.Revision, bindings
	c.bindingsCheckedAt = c.now()
	return nil
}

func (c *routeCapability) admitsBinding(claims routeClaims) bool {
	c.bindingMu.RLock()
	defer c.bindingMu.RUnlock()
	binding, exists := c.bindings[bindingKey(claims.SessionID, claims.Provider)]
	return c.bindingRevision > 0 && c.now().Sub(c.bindingsCheckedAt) < 5*time.Second && exists && !binding.Blocked && binding.Mode == "managed" && binding.AccountID == claims.AccountID && binding.Revision == claims.BindingRevision
}

func publicCredentialID(key, ref string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte("account\x00" + ref))
	return "amc_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:18])
}
