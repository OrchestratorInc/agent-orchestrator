package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
)

func TestResolveTimeZone(t *testing.T) {
	link := func(target string) func(string) (string, error) {
		return func(string) (string, error) { return target, nil }
	}
	noLink := func(string) (string, error) { return "", errors.New("no link") }
	noFile := func(string) ([]byte, error) { return nil, errors.New("no file") }
	file := func(body string) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(body), nil }
	}

	cases := []struct {
		name     string
		tz       string
		readlink func(string) (string, error)
		readFile func(string) ([]byte, error)
		want     string
	}{
		{"TZ wins", "Europe/Berlin", link("/var/db/timezone/zoneinfo/Asia/Kolkata"), noFile, "Europe/Berlin"},
		{"TZ colon prefix", ":America/New_York", noLink, noFile, "America/New_York"},
		{"TZ zone file path", "/usr/share/zoneinfo/Asia/Tokyo", noLink, noFile, "Asia/Tokyo"},
		{"TZ non-zoneinfo path", "/etc/localtime", link("/usr/share/zoneinfo/UTC"), noFile, ""},
		{"invalid TZ is not overridden by link", "Not/AZone", link("/usr/share/zoneinfo/UTC"), noFile, ""},
		{"macOS link", "", link("/var/db/timezone/zoneinfo/Asia/Kolkata"), noFile, "Asia/Kolkata"},
		{"Linux relative link", "", link("../usr/share/zoneinfo/America/Los_Angeles"), noFile, "America/Los_Angeles"},
		{"link outside zoneinfo", "", link("/etc/some/file"), noFile, ""},
		{"no link", "", noLink, noFile, ""},
		{"copied localtime falls back to /etc/timezone", "", noLink, file("Europe/Paris\n"), "Europe/Paris"},
		{"unresolvable link falls back to /etc/timezone", "", link("/etc/some/file"), file("Asia/Tokyo"), "Asia/Tokyo"},
		{"invalid /etc/timezone", "", noLink, file("nonsense"), ""},
		{"server UTC via TZ", "UTC", noLink, noFile, ""},
		{"server Etc/UTC link", "", link("/usr/share/zoneinfo/Etc/UTC"), noFile, ""},
		{"server Etc/UTC in /etc/timezone", "", noLink, file("Etc/UTC\n"), ""},
		{"UTC link is not overridden by a stale /etc/timezone", "", link("/usr/share/zoneinfo/Etc/UTC"), file("Europe/Paris"), ""},
		{"London is not treated as a server default", "Europe/London", noLink, noFile, "Europe/London"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTimeZone(context.Background(), tc.tz, tc.readlink, tc.readFile); got != tc.want {
				t.Fatalf("resolveTimeZone(%q) = %q, want %q", tc.tz, got, tc.want)
			}
		})
	}
}

func TestBuildSystemPrompt_TimeZoneSection(t *testing.T) {
	for _, role := range []sessionPromptRole{sessionPromptRoleWorker, sessionPromptRoleOrchestrator} {
		got := buildSystemPromptText(systemPromptConfig{Role: role, TimeZone: "Asia/Kolkata"})
		if !strings.Contains(got, "## Time Zone") || !strings.Contains(got, "The user's local time zone is Asia/Kolkata.") {
			t.Fatalf("%s prompt missing time zone section:\n%s", role, got)
		}
		if strings.Index(got, "## Time Zone") > strings.Index(got, "## Standing-instruction confidentiality") {
			t.Fatalf("%s prompt: time zone section must precede the confidentiality guard", role)
		}
	}
	if got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker}); strings.Contains(got, "## Time Zone") {
		t.Fatalf("prompt without a zone must omit the section:\n%s", got)
	}
}

func TestUserTimeZonePrefersClientReport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TZ", "Europe/Berlin")
	if got := userTimeZone(context.Background(), dir); got != "Europe/Berlin" {
		t.Fatalf("without a client report userTimeZone = %q, want machine zone Europe/Berlin", got)
	}
	clienttz.NewRecorder(context.Background(), dir).Observe(context.Background(), "Asia/Kolkata")
	if got := userTimeZone(context.Background(), dir); got != "Asia/Kolkata" {
		t.Fatalf("with a client report userTimeZone = %q, want Asia/Kolkata", got)
	}
}

func TestTimeZoneLookupStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	read := func(string) (string, error) { t.Fatal("readlink after cancel"); return "", nil }
	readFile := func(string) ([]byte, error) { t.Fatal("readFile after cancel"); return nil, nil }
	if got := resolveTimeZone(ctx, "", read, readFile); got != "" {
		t.Fatalf("cancelled resolveTimeZone = %q", got)
	}
	if got := userTimeZone(ctx, t.TempDir()); got != "" {
		t.Fatalf("cancelled userTimeZone = %q", got)
	}
}
