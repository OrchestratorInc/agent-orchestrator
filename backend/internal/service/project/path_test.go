package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestNormalizeGitReportedPathLineEndings(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, " leading repository")
	type testCase struct {
		name     string
		reported string
		want     string
	}
	cases := []testCase{
		{name: "LF", reported: path + "\n", want: path},
		{name: "no terminator", reported: path, want: path},
		{name: "relative LF", reported: " leading repository\n", want: path},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			testCase{name: "CRLF", reported: path + "\r\n", want: path},
			testCase{name: "forward slash CRLF", reported: filepath.ToSlash(path) + "\r\n", want: path},
		)
	} else {
		for _, suffix := range []string{"\r", "\r\r", "\n", " "} {
			cases = append(cases, testCase{name: "preserve " + suffix, reported: path + suffix + "\n", want: path + suffix})
		}
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGitReportedPath(base, tt.reported); got != tt.want {
				t.Fatalf("normalizeGitReportedPath(%q, %q) = %q; want %q", base, tt.reported, got, tt.want)
			}
		})
	}
}

func TestIsGitRepoPreservesPathCharacters(t *testing.T) {
	names := []string{"repository", " leading repository"}
	if runtime.GOOS != "windows" {
		names = append(names, "carriage\r", "carriages\r\r", "line\n", "trailing ")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, name)
			if out, err := exec.Command("git", "init", "-b", "main", path).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v (%s)", err, out)
			}
			if !isGitRepo(path) {
				t.Fatalf("isGitRepo(%q) = false; want true", path)
			}
			subdir := filepath.Join(path, "subdir")
			if err := os.Mkdir(subdir, 0o755); err != nil {
				t.Fatal(err)
			}
			if isGitRepo(subdir) {
				t.Fatalf("isGitRepo(%q) = true; want false", subdir)
			}
			if trimmed := strings.TrimSpace(name); trimmed != name && isGitRepo(filepath.Join(base, trimmed)) {
				t.Fatal("isGitRepo accepted a nonexistent trimmed sibling")
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "absolute", raw: home, want: home},
		{name: "relative", raw: "repository", want: "repository"},
		{name: "clean", raw: "./parent/../repository/", want: "repository"},
		{name: "leading space", raw: " repository", want: " repository"},
		{name: "trailing space", raw: "repository ", want: "repository "},
		{name: "surrounding spaces", raw: " repository ", want: " repository "},
		{name: "explicit whitespace directory", raw: "./   ", want: "   "},
		{name: "home", raw: "~", want: home},
		{name: "home child", raw: "~/repository ", want: filepath.Join(home, "repository ")},
		{name: "backslash home child", raw: `~\repository `, want: filepath.Join(home, "repository ")},
		{name: "literal tilde with space", raw: "~ ", want: "~ "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want, err := filepath.Abs(tt.want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := normalizePath(tt.raw)
			if err != nil || got != want {
				t.Fatalf("normalizePath(%q) = %q, %v; want %q", tt.raw, got, err, want)
			}
		})
	}
	for _, raw := range []string{"", " ", "\t\r\n", "\u2003"} {
		t.Run("blank="+raw, func(t *testing.T) {
			got, err := normalizePath(raw)
			var apiError *apierr.Error
			if got != "" || !errors.As(err, &apiError) || apiError.Code != "PATH_REQUIRED" {
				t.Fatalf("normalizePath(%q) = %q, %v; want PATH_REQUIRED", raw, got, err)
			}
		})
	}
}
