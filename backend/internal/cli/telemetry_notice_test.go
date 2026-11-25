package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestTelemetryNoticeShowsOncePerInstall(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	maybeShowTelemetryNotice(&out, envFrom(nil), dir, true)
	text := out.String()
	for _, want := range []string{"Settings > General > Privacy", "DO_NOT_TRACK=1", "docs/telemetry.md", "GitHub username only if you turn"} {
		if !strings.Contains(text, want) {
			t.Fatalf("notice missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, telemetryNoticeFile)); err != nil {
		t.Fatalf("notice marker not written: %v", err)
	}
	out.Reset()
	maybeShowTelemetryNotice(&out, envFrom(nil), dir, true)
	if out.Len() != 0 {
		t.Fatalf("notice shown twice: %q", out.String())
	}
}

func TestTelemetryNoticeSkipsNonHumanAndOptedOutRuns(t *testing.T) {
	cases := map[string]struct {
		env         map[string]string
		optedOut    bool
		interactive bool
	}{
		"piped output":  {interactive: false},
		"agent session": {env: map[string]string{"AO_SESSION_ID": "ao-12"}, interactive: true},
		"do not track":  {env: map[string]string{"DO_NOT_TRACK": "1"}, interactive: true},
		"opted out":     {optedOut: true, interactive: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.optedOut {
				if err := os.WriteFile(filepath.Join(dir, telemetryOptOutFile), []byte("1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			maybeShowTelemetryNotice(&out, envFrom(tc.env), dir, tc.interactive)
			if out.Len() != 0 {
				t.Fatalf("notice shown: %q", out.String())
			}
			if _, err := os.Stat(filepath.Join(dir, telemetryNoticeFile)); !os.IsNotExist(err) {
				t.Fatal("skipped run spent the one-time notice")
			}
		})
	}
}
