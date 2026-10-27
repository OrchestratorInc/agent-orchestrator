package reviewpolicy

import (
	"strings"
	"testing"
)

const worker = "w-1"

func shell(cmd string) Call { return Call{Kind: KindShell, Tool: "Bash", Command: cmd} }

// TestEvaluateDeniesReviewerEscapes is the #6842 probe table: every way a
// reviewer has been seen, or could be expected, to escape review-only.
func TestEvaluateDeniesReviewerEscapes(t *testing.T) {
	grant := Grant{WorkerSessionID: worker}
	probes := []Call{
		// Session management through the AO CLI.
		shell(`ao spawn --project p --harness claude-code --name x --prompt "do it"`),
		shell(`ao send --session w-1 --message "hi"`),
		shell(`ao session kill w-2`),
		shell(`ao stop`),
		shell(`ao review trigger w-1`),
		shell(`ao review submit --session w-2 --body review.md`),
		shell(`printf '%s' '{"reviews":[]}' | ao review submit --session w-2 --reviews -`),
		// Writes and pushes.
		shell(`printf 'x' > /tmp/out.txt`),
		shell(`echo x >> README.md`),
		shell(`git -C . commit --allow-empty -m x`),
		shell(`git -C . push origin HEAD:refs/heads/x`),
		shell(`git --git-dir=.git push origin HEAD`),
		shell(`git --work-tree=. checkout -- .`),
		shell(`git -c core.pager=sh diff`),
		shell(`git commit -m x`),
		shell(`git push origin HEAD`),
		shell(`git checkout main`),
		shell(`git reset --hard HEAD~1`),
		shell(`git stash`),
		shell(`git diff --output=/tmp/patch`),
		shell(`git log --output /tmp/log`),
		shell(`git diff --ext-diff`),
		shell(`git show --textconv HEAD`),
		shell(`git grep -O vim needle`),
		// Chaining, substitution, expansion.
		shell(`git diff; rm -rf .`),
		shell(`git diff && git push`),
		shell(`git diff || true`),
		shell(`git status & ao spawn`),
		shell("git diff\nao spawn --name x"),
		shell(`git show $(ao spawn)`),
		shell("git show `ao spawn`"),
		shell(`cat "$HOME/.ao/token"`),
		shell(`ls *.go`),
		shell(`cat ~/.ssh/id_rsa`),
		shell(`FOO=1 git diff`),
		shell(`/usr/bin/git push`),
		shell(`curl -X POST http://127.0.0.1:3001/api/v1/sessions`),
		shell(`gh api --method DELETE repos/o/r/git/refs/heads/main`),
		shell(`gh pr merge 1`),
		shell(`gh pr review 1 --approve`),
		shell(`rm -rf .`),
		shell(`python3 -c 'open("x","w")'`),
		shell(`sh -c 'git push'`),
		// Read tools in forms that would write or run programs.
		shell(`rg --pre cat needle`),
		shell(`rg --pre=./run.sh needle`),
		shell(`rg -z needle`),
		shell(`rg -nz needle`),
		shell(`sed -i s/a/b/ README.md`),
		shell(`sed -n 1w/tmp/out README.md`),
		shell(`sed s/a/b/ README.md`),
		shell(`sed -n 1,20p -i README.md`),
		shell(`find . -delete`),
		shell(`find . -name x -fprint /tmp/out`),
		shell(`find . -execdir rm x`),
		shell(`git status 'unterminated`),
		shell(``),
		// Non-shell escapes.
		{Kind: KindWrite, Tool: "Write"},
		{Kind: KindWrite, Tool: "Edit"},
		{Kind: KindSubagent, Tool: "Agent"},
		{Kind: KindNetwork, Tool: "WebFetch"},
		{Kind: KindMCP, Tool: "mcp__github__create_pr"},
		{Kind: KindUnknown, Tool: "SomethingNew"},
	}
	for _, call := range probes {
		got := Evaluate(call, grant)
		if got.Allow {
			t.Errorf("Evaluate(%s %q) allowed; want denied", call.Tool, call.Command)
			continue
		}
		if !strings.HasPrefix(got.Reason, DenyPrefix) {
			t.Errorf("Evaluate(%s %q) reason %q lacks %q", call.Tool, call.Command, got.Reason, DenyPrefix)
		}
	}
}

// TestEvaluateAllowsLegitimateReview pins every shape a real review needs, so
// tightening the policy can never silently break reviews.
func TestEvaluateAllowsLegitimateReview(t *testing.T) {
	grant := Grant{WorkerSessionID: worker}
	allowed := []Call{
		{Kind: KindRead, Tool: "Read"},
		{Kind: KindRead, Tool: "Grep"},
		{Kind: KindRead, Tool: "Glob"},
		{Kind: KindBookkeeping, Tool: "TodoWrite"},
		shell(`git status`),
		shell(`git status --short`),
		shell(`git diff main...HEAD`),
		shell(`git --no-pager diff HEAD~1 -- backend/internal/cli/hooks.go`),
		shell(`git log --oneline -20`),
		shell(`git log --format='%h %s' main..HEAD`),
		shell(`git show HEAD --stat`),
		shell(`git diff -- '*.go'`),
		shell(`git blame -L 10,20 README.md`),
		shell(`git grep -n reviewerPermissionDecision`),
		shell(`git merge-base main HEAD`),
		shell(`gh pr view 12 --json title,body`),
		shell(`gh pr diff 12`),
		shell(`gh pr checks 12`),
		shell(`cat README.md`),
		shell(`rg -n reviewerPermissionDecision backend`),
		shell(`rg --files -g '*.go'`),
		shell(`grep -rn needle .`),
		shell(`sed -n '1,200p' backend/internal/cli/hooks.go`),
		shell(`sed -n '$p' README.md`),
		shell(`nl -ba README.md`),
		shell(`find . -name '*.go' -type f`),
		shell(`ls -la backend`),
		shell(`printf '%s' '{ "event": "COMMENT", "body": "it'\''s fine", "comments": [] }' | gh api --method POST repos/o/r/pulls/12/reviews --input - --jq '.id'`),
		shell(`printf '%s' '{ "reviews": [ { "runId": "r1", "verdict": "approved", "body": "ok" } ] }' | ao review submit --session w-1 --reviews -`),
		shell(`ao review submit --session w-1 --body /tmp/review.md`),
		shell(`ao review submit --session=w-1 --body -`),
	}
	for _, call := range allowed {
		if got := Evaluate(call, grant); !got.Allow {
			t.Errorf("Evaluate(%s %q) denied: %s", call.Tool, call.Command, got.Reason)
		}
	}
}

func TestEvaluateWithoutGrantDeniesSubmit(t *testing.T) {
	for _, cmd := range []string{
		`ao review submit --session w-1 --body -`,
		`printf '%s' '{}' | ao review submit --session w-1 --reviews -`,
		`printf '%s' '{}' | gh api --method POST repos/o/r/pulls/1/reviews --input - --jq '.id'`,
	} {
		if got := Evaluate(shell(cmd), Grant{}); got.Allow {
			t.Errorf("Evaluate(%q) with no grant allowed; want denied", cmd)
		}
	}
}

func TestDecodeClaudeHookCall(t *testing.T) {
	call, err := DecodeClaudeHookCall([]byte(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`))
	if err != nil || call.Kind != KindShell || call.Command != "git status" {
		t.Fatalf("DecodeClaudeHookCall = %+v, %v", call, err)
	}
	if call, err := DecodeClaudeHookCall([]byte(`{"tool_name":"mcp__x__y"}`)); err != nil || call.Kind != KindMCP {
		t.Fatalf("mcp tool = %+v, %v", call, err)
	}
	for _, bad := range []string{``, `not json`, `{}`, `{"tool_name":"  "}`} {
		if _, err := DecodeClaudeHookCall([]byte(bad)); err == nil {
			t.Errorf("DecodeClaudeHookCall(%q) = nil error; want error", bad)
		}
	}
}

// TestPrefixListsStayInsideThePolicy keeps the prefix lists handed to
// prefix-matching harnesses consistent with Evaluate.
func TestPrefixListsStayInsideThePolicy(t *testing.T) {
	grant := Grant{WorkerSessionID: worker}
	for _, prefix := range ReadOnlyShellPrefixes() {
		cmd := prefix
		if prefix == "ao review submit" {
			cmd += " --session " + worker
		}
		if got := Evaluate(shell(cmd), grant); !got.Allow {
			t.Errorf("prefix %q is advertised but Evaluate denies it: %s", prefix, got.Reason)
		}
	}
	for _, fragment := range ForbiddenShellFragments() {
		if got := Evaluate(shell("git diff "+fragment+"x"), grant); got.Allow {
			t.Errorf("fragment %q is forbidden for prefix engines but Evaluate allows it", fragment)
		}
	}
}
