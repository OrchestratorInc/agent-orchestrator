// Package reviewpolicy is the single source of truth for what an AO reviewer
// may do. A reviewer reads a PR and records a verdict; it must never edit the
// workspace, push, or start or steer other sessions (#6842). Every reviewer
// harness enforces this one policy through its strongest native mechanism: a
// hook that asks Evaluate before each tool call, a configuration generated
// from the same allowlist, or a provider sandbox. The policy is an allowlist:
// anything it does not recognize is denied.
package reviewpolicy

import "strings"

// Kind is the harness-neutral category of a tool call. Each harness maps its
// own tool names onto these kinds before asking the policy.
type Kind string

const (
	// KindRead reads files without changing them (Read, Grep, Glob, LS).
	KindRead Kind = "read"
	// KindShell runs a shell command; Command carries the raw command line.
	KindShell Kind = "shell"
	// KindBookkeeping is harness-internal state with no effect outside the
	// agent (todo lists, tool search). It is allowed so a reviewer does not
	// trip over its own planning tools.
	KindBookkeeping Kind = "bookkeeping"
	// KindWrite edits or creates files.
	KindWrite Kind = "write"
	// KindSubagent starts another agent, which would not inherit this policy.
	KindSubagent Kind = "subagent"
	// KindNetwork fetches or searches the web.
	KindNetwork Kind = "network"
	// KindMCP calls a tool served by an MCP server.
	KindMCP Kind = "mcp"
	// KindUnknown is any tool the harness mapping does not recognize.
	KindUnknown Kind = "unknown"
)

// Call is one tool invocation a reviewer is about to make.
type Call struct {
	Kind Kind
	// Tool is the harness-native tool name, used only in denial messages.
	Tool string
	// Command is the raw shell command line for KindShell.
	Command string
}

// Grant scopes the policy to one review. A reviewer may submit results only
// for the worker session whose PR it was asked to review.
type Grant struct {
	WorkerSessionID string
}

// Decision is the policy's answer for one Call.
type Decision struct {
	Allow bool
	// Reason explains a denial to the model so it can continue with an
	// allowed tool instead of retrying the same call.
	Reason string
}

// DenyPrefix starts every denial message so operators and tests can tell an
// AO policy denial from a harness's own refusal.
const DenyPrefix = "AO reviewer policy: "

const allowedSummary = "Reviewers may only read files (Read, Grep, Glob), inspect git history " +
	"(git status/diff/log/show and other read-only git subcommands, one command per call, " +
	"no -C/--git-dir/-c/--output), read the PR with gh pr view/diff/checks, and record " +
	"the verdict with the exact `printf '%s' '<json>' | ...` submit commands from the review task."

func allow() Decision { return Decision{Allow: true} }

func deny(reason string) Decision {
	return Decision{Reason: DenyPrefix + reason + " " + allowedSummary}
}

// Evaluate decides whether a reviewer may make call under grant.
func Evaluate(call Call, grant Grant) Decision {
	switch call.Kind {
	case KindRead, KindBookkeeping:
		return allow()
	case KindShell:
		return evaluateShell(strings.TrimSpace(call.Command), grant)
	case KindWrite:
		return deny(toolLabel(call) + " is blocked: reviewers never modify files.")
	case KindSubagent:
		return deny(toolLabel(call) + " is blocked: reviewers cannot start other agents.")
	case KindNetwork:
		return deny(toolLabel(call) + " is blocked: reviewers do not use web tools.")
	case KindMCP:
		return deny(toolLabel(call) + " is blocked: reviewers do not call MCP tools.")
	default:
		return deny(toolLabel(call) + " is not a reviewer tool.")
	}
}

func toolLabel(call Call) string {
	if tool := strings.TrimSpace(call.Tool); tool != "" {
		return "The " + tool + " tool"
	}
	return "This tool"
}
