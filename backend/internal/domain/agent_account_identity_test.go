package domain

import (
	"strings"
	"testing"
)

func TestAgentAccountIdentitySanitized(t *testing.T) {
	if got := (AgentAccountIdentity{}).Sanitized(); got != nil {
		t.Fatalf("empty identity = %+v, want nil", got)
	}
	got := AgentAccountIdentity{Email: "  dev@example.com ", Name: "Dev\x07", Plan: strings.Repeat("x", 255), Organization: "Acme", Method: "oauth"}.Sanitized()
	want := AgentAccountIdentity{Email: "dev@example.com", Organization: "Acme"}
	if got == nil || *got != want {
		t.Fatalf("Sanitized() = %+v, want %+v", got, want)
	}
	if got := (AgentAccountIdentity{Email: "not-an-email"}).Sanitized(); got != nil {
		t.Fatalf("non-email = %+v, want nil", got)
	}
}
