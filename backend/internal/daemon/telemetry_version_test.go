package daemon

import "testing"

func TestTelemetryAppVersion(t *testing.T) {
	for _, tc := range []struct {
		name, configured, build, want string
	}{
		{"supervisor wins", "0.11.2", "0.14.0", "0.11.2"},
		{"daemon binary fallback", "", "0.14.0", "0.14.0"},
		{"unstamped build", "", "dev", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := telemetryAppVersion(tc.configured, tc.build); got != tc.want {
				t.Fatalf("telemetryAppVersion = %q, want %q", got, tc.want)
			}
		})
	}
}
