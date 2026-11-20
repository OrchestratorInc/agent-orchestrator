package cursor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestReviewerProfileIsActuallyRead pins the #7297 fix: Cursor CLI 2026.10.22
// reads cli-config.json from CURSOR_CONFIG_DIR (CURSOR_DATA_DIR alone left the
// reviewer on the user's own rules), so the env must point at the folder
// installReviewerConfig writes, not merely agree with CURSOR_DATA_DIR.
func TestReviewerProfileIsActuallyRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	inv := ports.ReviewInvocation{ReviewerID: "review-w1", DataDir: t.TempDir()}
	if err := installReviewerConfig(context.Background(), inv); err != nil {
		t.Fatalf("installReviewerConfig: %v", err)
	}

	env := reviewerEnv(inv)
	data, err := os.ReadFile(filepath.Join(env[cursorConfigDirEnv], cursorConfigFileName))
	if err != nil {
		t.Fatalf("CURSOR_CONFIG_DIR %q does not hold the written profile: %v", env[cursorConfigDirEnv], err)
	}
	if env[cursorDataDirEnv] != env[cursorConfigDirEnv] {
		t.Fatalf("reviewer env = %#v; both Cursor dirs must point at the AO profile", env)
	}
	var got reviewerConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if !reflect.DeepEqual(got.Permissions.Allow, reviewerAllowedPermissions) ||
		!reflect.DeepEqual(got.Permissions.Deny, reviewerDeniedPermissions) {
		t.Fatalf("profile permissions = %#v, want the reviewer rules", got.Permissions)
	}
}

// TestReviewerRulesRunThePromptAndKeepTheDenies checks the rules the way
// Cursor CLI 2026.10.22 applies them (verified live): every part of a command
// line must match an allow rule, and any part matching a deny rule blocks the
// whole line. The review prompt's two submit pipes must run, or every
// unattended Cursor review stalls at an approval prompt.
func TestReviewerRulesRunThePromptAndKeepTheDenies(t *testing.T) {
	for _, rule := range reviewerAllowedPermissions {
		switch rule {
		case "Shell(git)", "Shell(gh)", "Shell(ao)":
			t.Fatalf("allowlist admits a whole tool: %q", rule)
		}
	}
	for _, cmd := range []string{
		`printf '%s' '{ "event": "COMMENT", "body": "x" }' | gh api --method POST repos/o/r/pulls/1/reviews --input - --jq '.id'`,
		`printf '%s' '{ "reviews": [] }' | ao review submit --session sess-1 --reviews -`,
		"git diff origin/main...HEAD",
		"git fetch origin main",
	} {
		if !cursorRulesAllow(cmd) {
			t.Fatalf("rules do not allow prompt-required command %q", cmd)
		}
	}
	for _, cmd := range []string{
		"git push origin HEAD",
		"git status && git push origin HEAD",
		"git status; git commit -m x",
		`printf '%s' '{}' | ao spawn --prompt x`,
		"ao send w1 do-it",
		"ao stop",
		"gh pr merge 1",
	} {
		if cursorRulesAllow(cmd) {
			t.Fatalf("rules unexpectedly allow %q", cmd)
		}
	}
}

// cursorRulesAllow mirrors Cursor's shell rule check for the plain command
// lines used above: split on `&&`, `;` and `|`, deny if any part matches a
// deny rule, and allow only when every part matches an allow rule.
func cursorRulesAllow(command string) bool {
	parts := strings.FieldsFunc(strings.ReplaceAll(command, "&&", ";"), func(r rune) bool {
		return r == ';' || r == '|'
	})
	matches := func(rules []string, part string) bool {
		for _, rule := range rules {
			pattern, ok := strings.CutPrefix(rule, "Shell(")
			if !ok {
				continue
			}
			pattern = strings.TrimSuffix(pattern, ")")
			if part == pattern || strings.HasPrefix(part, pattern+" ") {
				return true
			}
		}
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if matches(reviewerDeniedPermissions, part) || !matches(reviewerAllowedPermissions, part) {
			return false
		}
	}
	return true
}
