package testfixture

import (
	"strings"
	"testing"
)

func TestSafeDiagnosticRedactsBearerBeforeAuthorizationField(t *testing.T) {
	input := "Authorization: Bearer ABC123==\napiKey: TOPSECRET\n" +
		`{"refresh_token":"REFRESH123","password":"PASSWORD123"} ao-fixture-key`
	got := SafeDiagnostic(input, 2048)
	for _, secret := range []string{"ABC123", "TOPSECRET", "REFRESH123", "PASSWORD123", "ao-fixture-key"} {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostic retained a synthetic secret %q", secret)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatal("diagnostic did not record redaction")
	}
}

func TestSafeDiagnosticBoundsAfterRedacting(t *testing.T) {
	if got := SafeDiagnostic("ordinary event details", 8); got != "ordinary..." {
		t.Fatalf("bounded diagnostic = %q", got)
	}
	got := SafeDiagnostic("Bearer "+strings.Repeat("S", 1024), 32)
	if strings.Contains(got, "SSSS") || len(got) > 35 {
		t.Fatalf("redaction/bounding failed: length=%d", len(got))
	}
}
