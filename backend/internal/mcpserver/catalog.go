package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	maxToolResultChars = 48_000
	maxTurnTextChars   = 6_000
	fullAccessMode     = "bypass-permissions"
)

type catalogTool struct {
	name, title, desc string
	act, ungated      bool
	props             map[string]any
	required          []string
	call              func(ctx context.Context, s *server, args map[string]any) (any, error)
}

type routeTool struct {
	method, path string
	query        []string
	rename       map[string]string
	defaults     map[string]any
	body         func(caller string, args map[string]any) map[string]any
	pick         func(map[string]any) any
}

func callerSession() string { return strings.TrimSpace(os.Getenv("AO_SESSION_ID")) }

func (t catalogTool) definition() map[string]any {
	props := t.props
	if props == nil {
		props = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(t.required) > 0 {
		schema["required"] = t.required
	}
	return map[string]any{
		"name": t.name, "title": t.title, "description": t.desc, "inputSchema": schema,
		"annotations": map[string]any{"title": t.title, "readOnlyHint": !t.act, "destructiveHint": false, "openWorldHint": false},
	}
}

func catalogDefinitions() []map[string]any {
	defs := make([]map[string]any, 0, len(catalog))
	for _, t := range catalog {
		defs = append(defs, t.definition())
	}
	return defs
}

func findCatalogTool(name string) (catalogTool, bool) {
	for _, t := range catalog {
		if t.name == name {
			return t, true
		}
	}
	return catalogTool{}, false
}

func (s *server) callCatalog(ctx context.Context, t catalogTool, raw json.RawMessage) map[string]any {
	args := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcpToolError(fmt.Errorf("invalid arguments: %w", err))
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	for name, value := range args {
		prop, declared := t.props[name].(map[string]any)
		if !declared || value == nil {
			delete(args, name)
			continue
		}
		kind, _ := prop["type"].(string)
		valid := true
		switch kind {
		case "string":
			var text string
			if text, valid = value.(string); valid && strings.TrimSpace(text) == "" {
				delete(args, name)
			}
		case "integer":
			var number float64
			number, valid = value.(float64)
			valid = valid && number == math.Trunc(number)
		case "boolean":
			_, valid = value.(bool)
		case "array":
			_, valid = value.([]any)
		case "object":
			_, valid = value.(map[string]any)
		}
		if !valid {
			return mcpToolError(fmt.Errorf("%s must be of type %s", name, kind))
		}
	}
	for _, name := range t.required {
		if _, ok := args[name]; !ok {
			return mcpToolError(fmt.Errorf("%s is required", name))
		}
	}
	if t.act && !t.ungated {
		if err := s.requireFullAccess(ctx); err != nil {
			return mcpToolError(err)
		}
	}
	result, err := t.call(ctx, s, args)
	if err != nil {
		return mcpToolError(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return mcpToolError(err)
	}
	text := string(encoded)
	if len(text) > maxToolResultChars {
		text = strings.ToValidUTF8(text[:maxToolResultChars], "") + fmt.Sprintf("\n[cut: %d of %d bytes shown, so this is not complete JSON; narrow the request]", maxToolResultChars, len(encoded))
	}
	return map[string]any{"content": []any{mcpText(text)}}
}

func (s *server) requireFullAccess(ctx context.Context) error {
	caller := callerSession()
	if caller == "" {
		return errors.New("this tool must run inside an Orchestrator session (AO_SESSION_ID is not set)")
	}
	var conversation struct {
		Settings struct {
			ApprovalMode string `json:"approvalMode"`
			PlanMode     bool   `json:"planMode"`
		} `json:"settings"`
	}
	if err := s.daemon.DoJSON(ctx, http.MethodGet, "sessions/"+url.PathEscape(caller)+"/conversation?limit=1", nil, &conversation); err != nil {
		return fmt.Errorf("could not read this session's permission mode: %w", err)
	}
	if mode := conversation.Settings.ApprovalMode; mode != fullAccessMode {
		if mode == "" {
			mode = "default"
		}
		return fmt.Errorf("this session's permission mode is %q. Tools that start or change sessions need full access (%s). "+
			"Ask the user to switch this session to full access, or to do this step in Orchestrator", mode, fullAccessMode)
	}
	if conversation.Settings.PlanMode {
		return errors.New("this session is in plan mode. Tools that start or change sessions do not run while it plans")
	}
	return nil
}

func (r routeTool) call(ctx context.Context, s *server, args map[string]any) (any, error) {
	caller := callerSession()
	path := r.path
	for _, name := range []string{"sessionId", "requestId", "automationId", "agent"} {
		placeholder := "{" + name + "}"
		if !strings.Contains(path, placeholder) {
			continue
		}
		value, _ := args[name].(string)
		if _, given := args[name]; !given && name == "sessionId" {
			value = caller
		}
		if value = strings.TrimSpace(value); value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
		path = strings.ReplaceAll(path, placeholder, url.PathEscape(value))
		delete(args, name)
	}
	for name, value := range r.defaults {
		if _, given := args[name]; !given {
			args[name] = value
		}
	}
	query := url.Values{}
	for _, name := range r.query {
		if value, ok := args[name]; ok && value != nil && value != "" {
			key := name
			if renamed := r.rename[name]; renamed != "" {
				key = renamed
			}
			query.Set(key, fmt.Sprint(value))
		}
		delete(args, name)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var body any
	if r.method != http.MethodGet {
		for from, to := range r.rename {
			if value, ok := args[from]; ok {
				args[to] = value
				delete(args, from)
			}
		}
		if r.body != nil {
			for key, value := range r.body(caller, args) {
				args[key] = value
			}
		}
		body = args
	}
	var out json.RawMessage
	if err := s.daemon.DoJSON(ctx, r.method, path, body, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return map[string]any{"ok": true}, nil
	}
	if r.pick == nil {
		return out, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		return out, nil
	}
	return r.pick(decoded), nil
}

func keep(source map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := source[key]; ok && value != nil && value != "" {
			out[key] = value
		}
	}
	return out
}

func each(value any, pick func(map[string]any) map[string]any) []any {
	items, _ := value.([]any)
	out := make([]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			out = append(out, pick(object))
		}
	}
	return out
}

func sessionSummary(session map[string]any) map[string]any {
	out := keep(session, "id", "displayName", "status", "kind", "harness", "mode", "model", "projectId", "branch",
		"isTerminated", "provisionState", "provisionError", "lastEventAt")
	if activity, ok := session["activity"].(map[string]any); ok {
		if state, _ := activity["state"].(string); state != "" {
			out["activity"] = state
		}
	}
	return out
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + fmt.Sprintf(" [cut: %d more bytes]", len(text)-limit)
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string, minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum, "description": desc}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

var (
	sessionProp      = stringProp("Session id. Defaults to this session.")
	otherSessionProp = stringProp("Session id, from session_list or session_spawn.")
	requestIDProp    = stringProp("A stable id for this request. A retry with the same id returns the same result instead of doing the work twice.")
)

func routed(name, title, desc string, act bool, props map[string]any, required []string, r routeTool) catalogTool {
	return catalogTool{name: name, title: title, desc: desc, act: act, props: props, required: required, call: r.call}
}

func ownSession(t catalogTool) catalogTool {
	t.ungated = true
	return t
}

func reviewTriggerBody(caller string, args map[string]any) map[string]any {
	rerun, _ := args["rerun"].(bool)
	return map[string]any{"requesterSessionId": caller, "source": "agent", "rejectReviewedHead": !rerun, "enableAutoInject": true}
}

var catalog = []catalogTool{
	{
		name: "capabilities", title: "List agents and accounts",
		desc: "Lists the coding agents installed on this computer, whether each is signed in, and the subscription accounts. " +
			"Call it before session_spawn when you choose an agent or model.",
		call: callCapabilities,
	},
	routed("agent_models", "List an agent's models", "Lists the models one coding agent offers.", false,
		map[string]any{"agent": stringProp("Agent id from capabilities, for example claude-code or codex."), "projectId": stringProp("Project id.")},
		[]string{"agent"},
		routeTool{method: http.MethodGet, path: "agents/{agent}/models", query: []string{"projectId"},
			pick: func(m map[string]any) any { return keep(m, "agentId", "models", "allowCustom") }}),
	routed("project_list", "List projects", "Lists the registered projects with their ids and paths.", false, nil, nil,
		routeTool{method: http.MethodGet, path: "projects", pick: func(m map[string]any) any {
			return map[string]any{"projects": each(m["projects"], func(p map[string]any) map[string]any {
				return keep(p, "id", "name", "path", "kind", "orchestratorAgent")
			})}
		}}),
	routed("session_list", "List sessions",
		"Lists sessions with status, agent, branch and project. Read it before you start new work, so you do not duplicate a session that exists.", false,
		map[string]any{
			"projectId": stringProp("Only sessions of this project."),
			"active":    map[string]any{"type": "boolean", "description": "Only sessions that are not terminated."},
			"kind":      enumProp("Only this kind.", "worker", "orchestrator"),
			"limit":     intProp("Sessions per page. Defaults to 50.", 1, 200),
			"cursor":    stringProp("nextCursor from the previous page."),
		}, nil,
		routeTool{method: http.MethodGet, path: "sessions", query: []string{"projectId", "active", "kind", "limit", "cursor"},
			rename: map[string]string{"projectId": "project"}, defaults: map[string]any{"limit": 50},
			pick: func(m map[string]any) any {
				out := keep(m, "nextCursor", "total")
				out["sessions"] = each(m["sessions"], sessionSummary)
				return out
			}}),
	routed("session_get", "Get a session", "Returns one session's status, activity, agent, model and branch.", false,
		map[string]any{"sessionId": sessionProp}, nil,
		routeTool{method: http.MethodGet, path: "sessions/{sessionId}", pick: func(m map[string]any) any {
			session, _ := m["session"].(map[string]any)
			return sessionSummary(session)
		}}),
	{
		name: "session_read", title: "Read a session's turns",
		desc: "Returns a session's turns, newest first: what was asked and what the agent answered. Use it to read a worker's result.",
		props: map[string]any{
			"sessionId": otherSessionProp,
			"limit":     intProp("Turns to return. Defaults to 5.", 1, 50),
			"offset":    intProp("Turns to skip from the newest.", 0, 100_000),
		},
		required: []string{"sessionId"},
		call:     callSessionRead,
	},
	routed("session_search", "Search past sessions", "Searches past sessions by title, message, tool output and touched files.", false,
		map[string]any{
			"query":     stringProp("Words to find."),
			"projectId": stringProp("Only sessions of this project."),
			"limit":     intProp("Results to return.", 1, 50),
		}, []string{"query"},
		routeTool{method: http.MethodGet, path: "search/sessions", query: []string{"query", "projectId", "limit"}, rename: map[string]string{"query": "q"}}),
	{
		name: "session_wait", title: "Wait for sessions",
		desc: "Waits until every listed session stops working: it is idle, needs input, has exited, or failed to start. " +
			"Use it instead of polling session_list. A timeout does not stop the sessions: timedOut is true and you can call it again.",
		props: map[string]any{
			"sessionIds": map[string]any{"type": "array", "minItems": 1, "maxItems": maxWaitSessions, "items": map[string]any{"type": "string"},
				"description": "Sessions to wait for."},
			"timeoutSec": intProp("Seconds to wait. Defaults to 50; 110 is the most one call can wait.", 1, maxWaitSeconds),
		},
		required: []string{"sessionIds"},
		call:     callSessionWait,
	},
	{
		name: "pending_list", title: "List pending requests",
		desc:     "Lists the approvals and questions a Chat session waits on. Each has a requestId, and an approval lists the decisions it offers.",
		props:    map[string]any{"sessionId": otherSessionProp},
		required: []string{"sessionId"},
		call:     callPendingList,
	},
	routed("pr_status", "Get a session's pull requests", "Returns the pull requests a session owns, with state, CI and review.", false,
		map[string]any{"sessionId": sessionProp}, nil,
		routeTool{method: http.MethodGet, path: "sessions/{sessionId}/pr", pick: func(m map[string]any) any {
			return map[string]any{"prs": each(m["prs"], func(pr map[string]any) map[string]any {
				return keep(pr, "number", "title", "state", "htmlUrl", "sourceBranch", "targetBranch", "headSha", "mergeability", "ci", "review")
			})}
		}}),
	routed("review_list", "List a session's reviews", "Lists Orchestrator's code reviews of a worker session's pull request.", false,
		map[string]any{"sessionId": sessionProp}, nil,
		routeTool{method: http.MethodGet, path: "sessions/{sessionId}/reviews", pick: func(m map[string]any) any { return keep(m, "reviews", "runs", "reviewerHarness") }}),
	routed("automation_list", "List automations", "Lists the recurring session automations.", false,
		map[string]any{"projectId": stringProp("Only automations of this project.")}, nil,
		routeTool{method: http.MethodGet, path: "automations", query: []string{"projectId"}}),
	routed("automation_runs", "List an automation's runs", "Lists the run history of one automation.", false,
		map[string]any{"automationId": stringProp("Automation id from automation_list."), "limit": intProp("Runs to return.", 1, 100)},
		[]string{"automationId"},
		routeTool{method: http.MethodGet, path: "automations/{automationId}/runs", query: []string{"limit"}}),

	{
		name: "session_spawn", title: "Start a session", act: true,
		desc: "Starts a new session with its own worktree and sends it the prompt. It returns as soon as the session exists; " +
			"call session_wait, then session_read, to get the result. The new session knows only the prompt: write a complete brief. " +
			"Pass requestId, so that a retry of this call does not start a second session. " +
			"The project, agent and model default to this session's project and that project's settings.",
		props: map[string]any{
			"prompt":      stringProp("The task, as a complete brief."),
			"projectId":   stringProp("Project id. Defaults to this session's project."),
			"kind":        enumProp("Defaults to worker.", "worker", "orchestrator"),
			"harness":     stringProp("Agent id from capabilities. Defaults to the project's agent for the kind."),
			"model":       stringProp("Model id from agent_models."),
			"effort":      stringProp("Reasoning effort, when the agent has one."),
			"mode":        enumProp("Interface. Defaults to the project's setting.", "chat", "tui"),
			"branch":      stringProp("Branch name for the session."),
			"baseBranch":  stringProp("Branch to start from. Defaults to the project's default branch."),
			"displayName": stringProp("Name shown in the sidebar."),
			"requestId":   requestIDProp,
		},
		required: []string{"prompt"},
		call:     callSessionSpawn,
	},
	routed("session_send", "Send a message to a session", "Sends a message to another session's agent. The agent reads it as its next instruction.", true,
		map[string]any{"sessionId": otherSessionProp, "message": stringProp("The message.")},
		[]string{"sessionId", "message"},
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/send",
			body: func(caller string, _ map[string]any) map[string]any { return map[string]any{"senderSessionId": caller} }}),
	routed("session_interrupt", "Interrupt a session's turn", "Stops the turn a Chat session runs now. The session stays, and a later message starts it again.", true,
		map[string]any{"sessionId": otherSessionProp}, []string{"sessionId"},
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/conversation/interrupt"}),
	routed("session_stop", "Stop a session", "Terminates a session. Its worktree and history stay, and session_restore brings it back.", true,
		map[string]any{"sessionId": otherSessionProp}, []string{"sessionId"},
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/kill",
			body: func(caller string, _ map[string]any) map[string]any { return map[string]any{"callerSessionId": caller} }}),
	routed("session_restore", "Restore a session", "Restores a terminated session, or resumes an agent that exited.", true,
		map[string]any{"sessionId": otherSessionProp}, []string{"sessionId"},
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/restore", pick: func(m map[string]any) any {
			out := keep(m, "ok", "restoreMode", "sessionId")
			if session, ok := m["session"].(map[string]any); ok {
				out["session"] = sessionSummary(session)
			}
			return out
		}}),
	ownSession(routed("session_rename", "Rename this session", "Sets the name this session shows in the sidebar.", true,
		map[string]any{"displayName": stringProp("The new name.")}, []string{"displayName"},
		routeTool{method: http.MethodPatch, path: "sessions/{sessionId}"})),
	{
		name: "session_fork", title: "Fork a session", act: true,
		desc:     "Copies a session into a new one that continues from the same conversation.",
		props:    map[string]any{"sessionId": otherSessionProp, "requestId": requestIDProp},
		required: []string{"sessionId"},
		call:     callSessionFork,
	},
	routed("session_switch", "Switch a session's agent", "Moves a session to another coding agent. The conversation continues there.", true,
		map[string]any{
			"sessionId": otherSessionProp,
			"harness":   stringProp("Agent id to switch to. It must differ from the session's current agent."),
			"model":     stringProp("Model id for the new agent."),
			"effort":    stringProp("Reasoning effort for the new agent."),
		}, []string{"sessionId", "harness"},
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/provider-switch", rename: map[string]string{"harness": "targetHarness"},
			pick: func(m map[string]any) any { return keep(m, "status", "warnings", "settingsUpdated", "stoppedTurn") }}),
	{
		name: "pending_respond", title: "Answer a pending request", act: true,
		desc: "Answers an approval or a question from pending_list. For an approval, give one of its decision ids. " +
			"For a question, give action accept with content, or decline.",
		props: map[string]any{
			"sessionId":  otherSessionProp,
			"requestId":  stringProp("requestId from pending_list."),
			"decisionId": stringProp("For an approval: the id of one offered decision."),
			"action":     enumProp("For a question.", "accept", "decline", "cancel"),
			"content":    map[string]any{"type": "object", "description": "For a question with action accept: the answer, in the shape its schema asks for."},
		},
		required: []string{"sessionId", "requestId"},
		call:     callPendingRespond,
	},
	ownSession(routed("pr_claim", "Attach a pull request to this session", "Attaches an existing pull request to this session, by number or URL. It changes ownership only and checks out nothing. "+
		"It does not take a pull request from a session that owns it; use ao session claim-pr for that.", true,
		map[string]any{"pr": stringProp("Pull request number or URL.")}, []string{"pr"},
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/pr/claim",
			body: func(string, map[string]any) map[string]any { return map[string]any{"allowTakeover": false} },
			pick: func(m map[string]any) any { return keep(m, "ok", "sessionId", "branchChanged", "takenOverFrom") }})),
	routed("review_trigger", "Start a code review", "Starts Orchestrator's code reviewer on a worker session's pull request.", true,
		map[string]any{"sessionId": sessionProp, "rerun": map[string]any{"type": "boolean", "description": "Review the same commit again."}}, nil,
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/reviews/trigger", body: reviewTriggerBody,
			pick: func(m map[string]any) any { return keep(m, "created", "reviews", "runs") }}),
	routed("review_cancel", "Cancel code reviews", "Cancels every running review of a worker session's pull request.", true,
		map[string]any{"sessionId": sessionProp}, nil,
		routeTool{method: http.MethodPost, path: "sessions/{sessionId}/reviews/cancel", pick: func(m map[string]any) any { return keep(m, "reviews") }}),
	routed("automation_create", "Create an automation", "Creates a recurring automation that starts a session on a schedule.", true,
		map[string]any{
			"projectId":   stringProp("Project id."),
			"displayName": stringProp("Name."),
			"prompt":      stringProp("What each run does."),
			"kind":        enumProp("Session kind for each run.", "worker", "orchestrator"),
			"timezone":    stringProp("IANA time zone, for example Asia/Kolkata."),
			"cron":        stringProp("Cron schedule. Give cron or rrule."),
			"rrule":       stringProp("RRULE schedule. Give cron or rrule."),
			"harness":     stringProp("Agent id."),
			"model":       stringProp("Model id."),
			"enabled":     map[string]any{"type": "boolean", "description": "Defaults to true."},
		}, []string{"projectId", "displayName", "prompt", "kind", "timezone"},
		routeTool{method: http.MethodPost, path: "automations"}),
	routed("automation_update", "Update an automation", "Changes an automation. Send only the fields that change; enabled false pauses it.", true,
		map[string]any{
			"automationId": stringProp("Automation id from automation_list."),
			"displayName":  stringProp("Name."),
			"prompt":       stringProp("What each run does."),
			"cron":         stringProp("Cron schedule."),
			"rrule":        stringProp("RRULE schedule."),
			"timezone":     stringProp("IANA time zone."),
			"harness":      stringProp("Agent id."),
			"model":        stringProp("Model id."),
			"enabled":      map[string]any{"type": "boolean", "description": "false pauses the automation."},
		}, []string{"automationId"},
		routeTool{method: http.MethodPatch, path: "automations/{automationId}"}),
	{
		name: "report", title: "Report progress", act: true, ungated: true,
		desc: "Records this worker's progress for the project's orchestrator: a checkpoint, a need for input, a blocker, or completion.",
		props: map[string]any{
			"state":   enumProp("What kind of report this is.", "checkpoint", "needs_input", "stuck", "done"),
			"note":    stringProp("What happened, in one or two sentences."),
			"message": stringProp("A free-form message for the orchestrator."),
		},
		required: []string{"state"},
		call:     callReport,
	},
}
