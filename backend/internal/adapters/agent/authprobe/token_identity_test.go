package authprobe

import (
	"encoding/base64"
	"testing"
)

func TestTokenIdentityReadsDisplayClaimsOnly(t *testing.T) {
	jwt := func(payload string) string {
		return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
	}
	for _, tt := range []struct {
		token, email, name string
	}{
		{jwt(`{"email":"dev@example.com","name":"Ada Lovelace"}`), "dev@example.com", "Ada Lovelace"},
		{jwt(`{"email":"dev@example.com","firstName":"Ada","lastName":"Lovelace"}`), "dev@example.com", "Ada Lovelace"},
		{jwt(`{"sub":"123"}`), "", ""},
		{"not-a-jwt", "", ""},
	} {
		if email, name := TokenIdentity(tt.token); email != tt.email || name != tt.name {
			t.Fatalf("TokenIdentity = (%q, %q), want (%q, %q)", email, name, tt.email, tt.name)
		}
	}
}
