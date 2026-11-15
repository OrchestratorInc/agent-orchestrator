//go:build !windows

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestChatE2EProviderStateFromOneTurn(t *testing.T) {
	requireE2E(t)

	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	projectID := seedProject(t, d, "provider-state")

	session := spawn(t, d, map[string]any{
		"projectId": projectID,
		"kind":      "worker",
		"harness":   "codex",
		"mode":      "chat",
	}).Session.ID

	d.mustCall("POST", "/sessions/"+session+"/conversation/messages", http.StatusAccepted,
		map[string]any{
			"text": "Use your plan tool (update_plan) for a three-step plan, then carry it out: " +
				"(1) append a line containing three to README.md, (2) create notes.md with two short " +
				"sentences, (3) run `ls -la` and report how many entries it printed. " +
				"Finish with one sentence.",
			"clientMessageId": "provider-state-1",
		}, nil)

	snap := d.awaitConversation(session, 5*time.Minute, "the turn to finish", func(s snapshot) bool {
		for _, turn := range s.Turns {
			if terminal(turn.State) {
				return true
			}
		}
		return false
	})

	var plan *turnPlan
	for _, turn := range snap.Turns {
		if turn.Plan != nil && len(turn.Plan.Steps) > 0 {
			plan = turn.Plan
		}
	}
	if plan == nil {
		t.Errorf("no turn carried a plan\n%s", describe(snap))
	} else {
		for i, step := range plan.Steps {
			if step.Text == "" {
				t.Errorf("plan step %d has no text: %+v", i, step)
			}
			switch step.Status {
			case "pending", "in_progress", "completed":
			default:
				t.Errorf("plan step %d status = %q, want AO's own spelling", i, step.Status)
			}
		}
		t.Logf("plan: %d steps, explanation=%q", len(plan.Steps), plan.Explanation)
		for i, step := range plan.Steps {
			t.Logf("  step %d [%s] %s", i, step.Status, trim(step.Text, 70))
		}
		if planRows := activitiesOfKind(snap, "plan"); len(planRows) > 1 {
			t.Errorf("plan produced %d timeline rows; the provider re-sends the whole plan, "+
				"so it must update one row", len(planRows))
		}
	}

	reasoning := activitiesOfKind(snap, "reasoning")
	if len(reasoning) == 0 {
		t.Logf("the provider reported no reasoning items for this turn")
	}
	t.Logf("%d reasoning rows", len(reasoning))
	for _, row := range reasoning {
		if row.Status == "running" {
			t.Errorf("reasoning row %s never settled", short(row.ID))
		}
		if text := detailString(t, row, "text"); text != "" {
			t.Logf("reasoning text: %q", trim(text, 90))
		}
	}

	changes := activitiesOfKind(snap, "file_change")
	if len(changes) == 0 {
		t.Fatalf("the agent was asked to edit two files and no file_change activity landed\n%s",
			describe(snap))
	}
	var sawPatch bool
	t.Logf("thread state: %+v", snap.ThreadState)
	for _, row := range changes {
		t.Logf("file change %q status=%s", trim(row.Summary, 70), row.Status)
		files := detailFiles(t, row)
		if len(files) == 0 {
			t.Errorf("file change %s carried no files: %s", short(row.ID), row.DetailJSON)
			continue
		}
		for _, file := range files {
			if file.Path == "" {
				t.Errorf("changed file has no path: %+v", file)
			}
			switch file.Status {
			case "added", "modified", "deleted", "renamed":
			default:
				t.Errorf("changed file status = %q, want AO's neutral spelling", file.Status)
			}
			if file.Patch != "" {
				sawPatch = true
			}
		}
		if row.Summary == "Edited files" && len(files) == 1 {
			t.Errorf("a single-file change was labelled generically: %q", row.Summary)
		}
	}
	if !sawPatch {
		t.Error("no file change carried patch text, so a client cannot render the diff " +
			"until the turn's aggregate arrives")
	}

	if snap.ThreadState == nil {
		t.Fatalf("the provider reported no thread state\n%s", describe(snap))
	}
	switch snap.ThreadState.Status {
	case "active", "idle":
	default:
		t.Errorf("thread status = %q after a finished turn", snap.ThreadState.Status)
	}
	if snap.ThreadState.ArchivedAt != nil {
		t.Errorf("a thread nobody archived reported archivedAt = %v", *snap.ThreadState.ArchivedAt)
	}

	if len(snap.MCPServers) == 0 {
		t.Logf("no MCP servers configured for this install")
	}
	for _, server := range snap.MCPServers {
		t.Logf("mcp server %q status=%q failure=%q", server.Name, server.Status, server.FailureReason)
		if server.Name == "" {
			t.Errorf("MCP server reported with no name: %+v", server)
		}
		switch server.Status {
		case "starting", "ready", "failed", "cancelled":
		default:
			t.Errorf("MCP server %q status = %q", server.Name, server.Status)
		}
	}

	if snap.ModelReroute != nil {
		t.Errorf("a turn nobody rerouted reported one: %+v", snap.ModelReroute)
	}
	if snap.Account != nil && snap.Account.ReauthRequiredAt != nil {
		t.Errorf("a working session claimed it needed re-authentication: %+v", snap.Account)
	}
}

func TestChatE2EReloadMCPServers(t *testing.T) {
	requireE2E(t)

	d := startDaemon(t, t.TempDir())
	projectID := seedProject(t, d, "mcp-reload")

	session := spawn(t, d, map[string]any{
		"projectId": projectID,
		"kind":      "worker",
		"harness":   "codex",
		"mode":      "chat",
	}).Session.ID
	d.awaitLiveController(session, 90*time.Second)

	var reloaded struct {
		Servers []mcpServerState `json:"servers"`
	}
	d.mustCall("POST", "/sessions/"+session+"/conversation/mcp/reload", http.StatusOK, nil, &reloaded)
	for _, server := range reloaded.Servers {
		if server.Name == "" {
			t.Errorf("reload reported a server with no name: %+v", server)
		}
	}
	t.Logf("reload reported %d tool servers", len(reloaded.Servers))

	d.mustCall("POST", "/sessions/"+session+"/conversation/messages", http.StatusAccepted,
		map[string]any{
			"text":            "Count slowly from one to twenty in words, one per line.",
			"clientMessageId": "mcp-reload-busy",
		}, nil)

	status, apiErr := d.callExpectingError("POST", "/sessions/"+session+"/conversation/mcp/reload", nil)
	switch {
	case status == http.StatusConflict && apiErr.Code == "CHAT_TURN_RUNNING":
		if strings.Contains(apiErr.Message, "rolling back") {
			t.Errorf("the busy refusal talks about rolling back: %q", apiErr.Message)
		}
	case status == http.StatusOK:
		t.Log("the turn completed before the busy reload landed; the refusal was not exercised")
	default:
		t.Errorf("busy reload -> %d %s %q", status, apiErr.Code, apiErr.Message)
	}
}

func activitiesOfKind(s snapshot, kind string) []activity {
	var out []activity
	for _, a := range s.Activities {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func detailString(t *testing.T, a activity, key string) string {
	t.Helper()
	if len(a.DetailJSON) == 0 {
		return ""
	}
	var detail map[string]any
	if err := json.Unmarshal(a.DetailJSON, &detail); err != nil {
		t.Errorf("activity %s detail is not an object: %v", short(a.ID), err)
		return ""
	}
	value, _ := detail[key].(string)
	return value
}

type detailFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

func detailFiles(t *testing.T, a activity) []detailFile {
	t.Helper()
	if len(a.DetailJSON) == 0 {
		return nil
	}
	var detail struct {
		Files []detailFile `json:"files"`
	}
	if err := json.Unmarshal(a.DetailJSON, &detail); err != nil {
		t.Errorf("activity %s detail is not an object: %v", short(a.ID), err)
		return nil
	}
	return detail.Files
}
