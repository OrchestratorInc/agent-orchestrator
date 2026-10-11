package controllers

import "testing"

func TestBrowserTelemetryActionAllowlist(t *testing.T) {
	tests := []struct {
		name   string
		action string
		want   string
	}{
		{name: "recognized action", action: " SNAPSHOT ", want: "snapshot"},
		{name: "unsupported action", action: "https://private.example/secret-token", want: "invalid"},
		{name: "oversized action", action: "snapshot" + string(make([]byte, 65)), want: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := browserTelemetryAction(tt.action); got != tt.want {
				t.Fatalf("browserTelemetryAction(%q) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}
