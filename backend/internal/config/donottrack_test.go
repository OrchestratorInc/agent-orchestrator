package config

import (
	"path/filepath"
	"testing"
)

func TestDoNotTrackForcesRemoteTelemetryAndSentryOff(t *testing.T) {
	for _, tc := range []struct {
		value string
		off   bool
	}{
		{"1", true}, {"true", true}, {"YES", true}, {" on ", true},
		{"", false}, {"0", false}, {"false", false}, {"maybe", false},
	} {
		t.Run("DO_NOT_TRACK="+tc.value, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("AO_DATA_DIR", filepath.Join(dir, "data"))
			t.Setenv("AO_RUN_FILE", filepath.Join(dir, "running.json"))
			t.Setenv("AO_TELEMETRY_EVENTS", "on")
			t.Setenv("AO_TELEMETRY_REMOTE", "posthog")
			t.Setenv("AO_SENTRY_DSN", "https://key@example.ingest.sentry.io/1")
			t.Setenv("DO_NOT_TRACK", tc.value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Telemetry.Remote == TelemetryRemoteOff && cfg.Telemetry.SentryDSN == ""; got != tc.off {
				t.Fatalf("remote=%q sentry=%q, want off=%v", cfg.Telemetry.Remote, cfg.Telemetry.SentryDSN, tc.off)
			}
			// Local event storage never leaves the machine and is unaffected.
			if !cfg.Telemetry.Events {
				t.Fatal("DO_NOT_TRACK turned off local event capture")
			}
		})
	}
}
