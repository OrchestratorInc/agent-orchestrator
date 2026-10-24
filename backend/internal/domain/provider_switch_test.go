package domain

import "testing"

func TestProviderSwitchLimits(t *testing.T) {
	if ProviderSwitchMaxBootstrapChars != 32_000 {
		t.Fatalf("bootstrap limit = %d, want 32000", ProviderSwitchMaxBootstrapChars)
	}
}
