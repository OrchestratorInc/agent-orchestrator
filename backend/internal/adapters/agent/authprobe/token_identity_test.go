package authprobe

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
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

func TestJSONProviderMethod(t *testing.T) {
	if got := JSONProviderMethod([]byte(`{"zai":{"type":"api","key":"k"}}`)); got != "api_key" {
		t.Fatalf("api entries = %q", got)
	}
	if got := JSONProviderMethod([]byte(`{"zai":{"type":"api"},"anthropic":{"type":"oauth"}}`)); got != "account" {
		t.Fatalf("oauth entry = %q", got)
	}
	if got := JSONProviderNames([]byte(`{"zai-glm":{},"zai-coding-cn":{},"mcp:github":{}}`)); got != "zai-coding-cn, zai-glm" {
		t.Fatalf("names = %q", got)
	}
}

func TestCodexAuthFileIdentityReadsTheIDToken(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@example.com","name":"Ada"}`))
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"id_token":"h.`+payload+`.s","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if email, name := CodexAuthFileIdentity(context.Background(), path); email != "dev@example.com" || name != "Ada" {
		t.Fatalf("CodexAuthFileIdentity = (%q, %q)", email, name)
	}
}
