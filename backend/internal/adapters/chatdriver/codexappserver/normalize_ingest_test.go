package codexappserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNormalizeReasoningSummaryStreams(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemReasoningSummaryTextDelta,
		`{"threadId":"019fc432","turnId":"019fc432-90af","itemId":"rs_00c2658b","delta":"**Searching deferred tool in ALL_TOOLS**","summaryIndex":0}`)
	if ev.Kind != ports.ChatEventReasoningDelta {
		t.Fatalf("kind = %q, want %q", ev.Kind, ports.ChatEventReasoningDelta)
	}
	if ev.Delta != "**Searching deferred tool in ALL_TOOLS**" {
		t.Fatalf("delta = %q", ev.Delta)
	}
	if ev.ProviderItemID != "rs_00c2658b" || ev.ProviderTurnID != "019fc432-90af" {
		t.Fatalf("ids = %q/%q", ev.ProviderItemID, ev.ProviderTurnID)
	}

	normalizeNone(t, codexproto.MethodItemReasoningSummaryTextDelta,
		`{"itemId":"rs_1","delta":"","summaryIndex":0}`)
}

func TestNormalizeReasoningSummaryPartBreaksOnlyBetweenParts(t *testing.T) {
	normalizeNone(t, codexproto.MethodItemReasoningSummaryPartAdded,
		`{"threadId":"th","turnId":"tu","itemId":"rs_1","summaryIndex":0}`)

	ev := normalizeOne(t, codexproto.MethodItemReasoningSummaryPartAdded,
		`{"threadId":"th","turnId":"tu","itemId":"rs_1","summaryIndex":1}`)
	if ev.Kind != ports.ChatEventReasoningDelta || ev.Delta != "\n\n" {
		t.Fatalf("part break -> %+v", ev)
	}
	if ev.ProviderItemID != "rs_1" {
		t.Fatalf("item id = %q", ev.ProviderItemID)
	}
}

func TestNormalizeRawReasoningDelta(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemReasoningTextDelta,
		`{"threadId":"th","turnId":"tu","itemId":"rs_1","contentIndex":0,"delta":"chain of thought"}`)
	if ev.Kind != ports.ChatEventReasoningDelta || ev.Delta != "chain of thought" {
		t.Fatalf("raw reasoning -> %+v", ev)
	}
}

func TestNormalizeReasoningItemCarriesItsSummaryText(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCompleted,
		`{"threadId":"th","turnId":"tu","completedAtMs":1785703001253,"item":{"type":"reasoning","id":"rs_1","summary":["**Rendering exact result**","**Second thought**"],"content":[]}}`)
	if ev.ActivityKind != domain.ActivityKindReasoning {
		t.Fatalf("kind = %q", ev.ActivityKind)
	}
	detail := decodeDetailMap(t, ev.Detail)
	if got := detail["text"]; got != "**Rendering exact result**\n\n**Second thought**" {
		t.Fatalf("reasoning text = %q", got)
	}
}

func TestNormalizeTurnPlanUpdated(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodTurnPlanUpdated,
		`{"threadId":"019fc430","turnId":"019fc430-d7ce","explanation":null,"plan":[{"step":"Append a line containing \"three\" to hello.txt","status":"completed"},{"step":"Create notes.md containing two short sentences","status":"completed"},{"step":"Run ls -la and report the file count","status":"inProgress"}]}`)

	if ev.Kind != ports.ChatEventPlanUpdated {
		t.Fatalf("kind = %q, want %q", ev.Kind, ports.ChatEventPlanUpdated)
	}
	if ev.ProviderTurnID != "019fc430-d7ce" {
		t.Fatalf("turn id = %q", ev.ProviderTurnID)
	}
	if ev.Plan == nil {
		t.Fatal("no plan on the event")
	}
	if len(ev.Plan.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(ev.Plan.Steps))
	}
	if ev.Plan.Steps[0].Status != domain.PlanStepCompleted {
		t.Errorf("step 0 status = %q", ev.Plan.Steps[0].Status)
	}
	if ev.Plan.Steps[2].Status != domain.PlanStepInProgress {
		t.Errorf("step 2 status = %q, want %q", ev.Plan.Steps[2].Status, domain.PlanStepInProgress)
	}
	if ev.Plan.Steps[2].Text != "Run ls -la and report the file count" {
		t.Errorf("step 2 text = %q", ev.Plan.Steps[2].Text)
	}
	if !strings.HasPrefix(ev.Summary, "Plan 2/3:") {
		t.Errorf("summary = %q, want a 2/3 progress label", ev.Summary)
	}
}

func TestNormalizePlanUnknownStatusIsPending(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodTurnPlanUpdated,
		`{"threadId":"th","turnId":"tu","plan":[{"step":"do it","status":"someFutureStatus"}]}`)
	if ev.Plan.Steps[0].Status != domain.PlanStepPending {
		t.Fatalf("status = %q, want %q", ev.Plan.Steps[0].Status, domain.PlanStepPending)
	}
}

func TestNormalizeEmptyPlanIsReported(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodTurnPlanUpdated, `{"threadId":"th","turnId":"tu","plan":[]}`)
	if ev.Plan == nil || len(ev.Plan.Steps) != 0 {
		t.Fatalf("plan -> %+v", ev.Plan)
	}
	if ev.Summary != "Cleared the plan" {
		t.Fatalf("summary = %q", ev.Summary)
	}
}

func TestNormalizePlanDeltaIsText(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemPlanDelta,
		`{"threadId":"th","turnId":"tu","itemId":"plan_1","delta":"1. first"}`)
	if ev.Kind != ports.ChatEventActivityText || ev.Delta != "1. first" {
		t.Fatalf("plan delta -> %+v", ev)
	}
}

func TestNormalizeFileChangeItemProducesNeutralFiles(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemStarted,
		`{"threadId":"th","turnId":"tu","startedAtMs":1785702980000,"item":{"type":"fileChange","id":"exec-adde2cb3","status":"inProgress","changes":[{"path":"/ws/hello.txt","kind":{"type":"update","move_path":null},"diff":"@@ -2 +2,2 @@\n two\n+three\n"},{"path":"/ws/notes.md","kind":{"type":"add"},"diff":"This is a note.\nIt is short.\n"}]}}`)

	if ev.Kind != ports.ChatEventActivityStarted || ev.ActivityKind != domain.ActivityKindFileChange {
		t.Fatalf("event -> %+v", ev)
	}
	if ev.Summary != "Edited 2 files" {
		t.Fatalf("summary = %q", ev.Summary)
	}

	files := decodeFiles(t, ev.Detail)
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if files[0].Path != "/ws/hello.txt" || files[0].Status != "modified" {
		t.Errorf("file 0 = %+v", files[0])
	}
	if files[0].Additions != 1 || files[0].Deletions != 0 {
		t.Errorf("file 0 counts = +%d/-%d, want +1/-0", files[0].Additions, files[0].Deletions)
	}
	if files[0].Patch == "" {
		t.Error("file 0 carried no patch text")
	}
	if files[1].Status != "added" || files[1].Additions != 2 || files[1].Deletions != 0 {
		t.Errorf("file 1 = %+v", files[1])
	}
}

func TestNormalizeFileChangeSummaryNamesOneFile(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCompleted,
		`{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"i","changes":[{"path":"notes.md","kind":{"type":"add"},"diff":"one\n"}]}}`)
	if ev.Summary != "Created notes.md" {
		t.Fatalf("summary = %q", ev.Summary)
	}
}

func TestNormalizeFileChangeMoveBecomesRename(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCompleted,
		`{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"i","changes":[{"path":"old.txt","kind":{"type":"update","move_path":"new.txt"},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}}`)
	files := decodeFiles(t, ev.Detail)
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	if files[0].Path != "new.txt" || files[0].OldPath != "old.txt" || files[0].Status != "renamed" {
		t.Fatalf("file = %+v", files[0])
	}
}

func TestNormalizePatchUpdatedUpdatesTheSameActivity(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemFileChangePatchUpdated,
		`{"threadId":"th","turnId":"tu","itemId":"exec-1","changes":[{"path":"a.py","kind":{"type":"update"},"diff":"@@ -1 +1,2 @@\n a\n+b\n"}]}`)
	if ev.Kind != ports.ChatEventActivityStarted {
		t.Fatalf("kind = %q, want the activity to be updated in place", ev.Kind)
	}
	if ev.ProviderItemID != "exec-1" || ev.ActivityKind != domain.ActivityKindFileChange {
		t.Fatalf("event -> %+v", ev)
	}
	if ev.ActivityStatus != domain.ActivityStatusRunning {
		t.Fatalf("status = %q: a restated patch is not a completion", ev.ActivityStatus)
	}
	if files := decodeFiles(t, ev.Detail); len(files) != 1 || files[0].Path != "a.py" {
		t.Fatalf("files = %+v", files)
	}
	normalizeNone(t, codexproto.MethodItemFileChangePatchUpdated,
		`{"threadId":"th","turnId":"tu","itemId":"exec-1","changes":[]}`)
}

func TestNormalizeFileChangeOutputDeltaIsText(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemFileChangeOutputDelta,
		`{"threadId":"th","turnId":"tu","itemId":"exec-1","delta":"applying patch\n"}`)
	if ev.Kind != ports.ChatEventActivityText || ev.Delta != "applying patch\n" {
		t.Fatalf("file change output -> %+v", ev)
	}
}

func TestNormalizeMcpToolCallReadsServerToolArgumentsAndResult(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCompleted,
		`{"threadId":"th","turnId":"tu","completedAtMs":1785703100000,"item":{"type":"mcpToolCall","id":"exec-b0ff0c94","server":"probe","tool":"slow_echo","status":"completed","arguments":{"subject":"hydraulics"},"appContext":null,"pluginId":null,"result":{"content":[{"type":"text","text":"probe tool finished"}],"structuredContent":null,"_meta":null},"error":null,"durationMs":3014}}`)

	if ev.ActivityKind != domain.ActivityKindMCPTool {
		t.Fatalf("kind = %q, want %q", ev.ActivityKind, domain.ActivityKindMCPTool)
	}
	if ev.Summary != "Called probe/slow_echo" {
		t.Fatalf("summary = %q", ev.Summary)
	}
	if ev.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("status = %q", ev.ActivityStatus)
	}

	detail := decodeDetailAny(t, ev.Detail)
	if detail["toolName"] != "slow_echo" || detail["server"] != "probe" {
		t.Fatalf("detail names = %+v", detail)
	}
	args, ok := detail["arguments"].(map[string]any)
	if !ok || args["subject"] != "hydraulics" {
		t.Fatalf("arguments = %#v", detail["arguments"])
	}
	if _, ok := detail["result"].(map[string]any); !ok {
		t.Fatalf("result = %#v", detail["result"])
	}
	if detail["durationMs"] != float64(3014) {
		t.Fatalf("durationMs = %#v", detail["durationMs"])
	}
}

func TestNormalizeMcpToolCallFailureIsFailed(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCompleted,
		`{"threadId":"th","turnId":"tu","item":{"type":"mcpToolCall","id":"i","server":"s","tool":"t","error":{"message":"tool exploded"}}}`)
	if ev.ActivityStatus != domain.ActivityStatusFailed {
		t.Fatalf("status = %q, want failed", ev.ActivityStatus)
	}
	if detail := decodeDetailMap(t, ev.Detail); detail["error"] != "tool exploded" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestNormalizeMcpToolPayloadOverCapIsMarkedNotCut(t *testing.T) {
	big := strings.Repeat("x", commanddetail.MaxToolPayloadChars+10)
	params := `{"threadId":"th","turnId":"tu","item":{"type":"mcpToolCall","id":"i","server":"s","tool":"t","arguments":{"blob":"` + big + `"}}}`
	ev := normalizeOne(t, codexproto.MethodItemCompleted, params)

	detail := decodeDetailAny(t, ev.Detail)
	args, ok := detail["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("arguments = %#v", detail["arguments"])
	}
	if args["truncated"] != true {
		t.Fatalf("oversized arguments were not marked truncated: %#v", args)
	}
	if _, stillThere := args["blob"]; stillThere {
		t.Fatal("oversized payload was stored anyway")
	}
}

func TestNormalizeCommandAggregatedOutputKeepsTail(t *testing.T) {
	item := func(output string) string {
		encoded, _ := json.Marshal(output)
		return `{"threadId":"th","turnId":"tu","item":{"type":"commandExecution","id":"exec-1","command":"yes",` +
			`"exitCode":0,"aggregatedOutput":` + string(encoded) + `}}`
	}
	tail := strings.Repeat("é", commanddetail.MaxOutputChars)
	detail := decodeDetailAny(t, normalizeOne(t, codexproto.MethodItemCompleted, item("lost head\n"+tail)).Detail)
	if detail["output"] != tail || detail["outputTruncated"] != true {
		output, _ := detail["output"].(string)
		t.Fatalf("output = %d chars truncated=%v, want the last %d chars, truncated",
			len([]rune(output)), detail["outputTruncated"], commanddetail.MaxOutputChars)
	}
	if detail["outputMayBePartial"] != true || detail["outputSource"] != "aggregate" {
		t.Fatalf("aggregate provenance lost: %v", detail["outputSource"])
	}

	small := decodeDetailAny(t, normalizeOne(t, codexproto.MethodItemCompleted, item("hi\n")).Detail)
	if small["output"] != "hi\n" {
		t.Fatalf("small output = %q", small["output"])
	}
	if _, marked := small["outputTruncated"]; marked {
		t.Fatal("small output was marked truncated")
	}
}

func TestNormalizeFileChangePatchOverCapIsCutOnCharacterBoundary(t *testing.T) {
	diff, _ := json.Marshal("+" + strings.Repeat("é", commanddetail.MaxPatchChars))
	ev := normalizeOne(t, codexproto.MethodItemCompleted,
		`{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"i","changes":[{"path":"accents.txt","kind":{"type":"add"},"diff":`+
			string(diff)+`}]}}`)
	files := decodeFiles(t, ev.Detail)
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	patch := files[0].Patch
	if !files[0].PatchTruncated || len(patch) > commanddetail.MaxPatchChars || len(patch) < commanddetail.MaxPatchChars-3 ||
		!strings.HasPrefix("+"+strings.Repeat("é", commanddetail.MaxPatchChars), patch) {
		t.Fatalf("patch = %d bytes truncated=%v, want a prefix of at most %d bytes", len(patch), files[0].PatchTruncated,
			commanddetail.MaxPatchChars)
	}
	if strings.ContainsRune(patch, '\uFFFD') {
		t.Fatal("patch was cut inside a character")
	}
}

func TestNormalizeMcpToolProgressIsActivityText(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemMcpToolCallProgress,
		`{"threadId":"th","turnId":"tu","itemId":"exec-1","message":"probe step 1 of 3"}`)
	if ev.Kind != ports.ChatEventActivityText {
		t.Fatalf("kind = %q", ev.Kind)
	}
	if ev.Delta != "probe step 1 of 3\n" {
		t.Fatalf("delta = %q, want a newline-terminated progress line", ev.Delta)
	}
	if ev.ProviderItemID != "exec-1" {
		t.Fatalf("item id = %q", ev.ProviderItemID)
	}
}

func TestNormalizeMcpServerStartupStatus(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodMcpServerStartupStatusUpdated,
		`{"threadId":"019fc432","name":"probe","status":"ready","error":null,"failureReason":null}`)
	if ev.Kind != ports.ChatEventMCPServers {
		t.Fatalf("kind = %q", ev.Kind)
	}
	if len(ev.MCPServers) != 1 {
		t.Fatalf("servers = %d", len(ev.MCPServers))
	}
	if ev.MCPServers[0].Name != "probe" || ev.MCPServers[0].Status != "ready" {
		t.Fatalf("server = %+v", ev.MCPServers[0])
	}

	failed := normalizeOne(t, codexproto.MethodMcpServerStartupStatusUpdated,
		`{"threadId":"th","name":"github","status":"failed","error":"connection refused","failureReason":"reauthenticationRequired"}`)
	if failed.MCPServers[0].Error != "connection refused" {
		t.Errorf("error = %q", failed.MCPServers[0].Error)
	}
	if failed.MCPServers[0].FailureReason != "reauthenticationRequired" {
		t.Errorf("failure reason = %q", failed.MCPServers[0].FailureReason)
	}

	normalizeNone(t, codexproto.MethodMcpServerStartupStatusUpdated, `{"threadId":"th","status":"ready"}`)
}

func TestNormalizeTerminalInteraction(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCommandExecutionTerminalInteraction,
		`{"threadId":"019fc435","turnId":"019fc435-ff11","itemId":"exec-cbf47a22","processId":"7409","stdin":"print(6*7)\n"}`)

	if ev.Kind != ports.ChatEventCommandInput {
		t.Fatalf("kind = %q, want %q", ev.Kind, ports.ChatEventCommandInput)
	}
	if ev.Delta != "print(6*7)\n" {
		t.Fatalf("delta = %q", ev.Delta)
	}
	if ev.ProviderItemID != "exec-cbf47a22" {
		t.Fatalf("item id = %q", ev.ProviderItemID)
	}
	if detail := decodeDetailMap(t, ev.Detail); detail["processId"] != "7409" {
		t.Fatalf("detail = %+v", detail)
	}

	normalizeNone(t, codexproto.MethodItemCommandExecutionTerminalInteraction,
		`{"itemId":"exec-1","processId":"1","stdin":""}`)
}

func TestNormalizeCommandItemCarriesProcessID(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemStarted,
		`{"threadId":"th","turnId":"tu","startedAtMs":1,"item":{"type":"commandExecution","id":"exec-1","command":"/bin/zsh -lc python3","cwd":"/ws","processId":"7409","source":"unifiedExecStartup","status":"inProgress"}}`)
	if detail := decodeDetailMap(t, ev.Detail); detail["processId"] != "7409" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestNormalizeAutoApprovalReview(t *testing.T) {
	started := normalizeOne(t, codexproto.MethodItemAutoApprovalReviewStarted,
		`{"threadId":"019fc434","turnId":"019fc434-39c6","startedAtMs":1785703203778,"reviewId":"5b95c779","targetItemId":"exec-de86ce2c","review":{"status":"inProgress","riskLevel":null,"userAuthorization":null,"rationale":null},"action":{"type":"command","source":"unifiedExec","command":"/bin/zsh -lc 'curl -s https://example.com'","cwd":"/ws"}}`)

	if started.Kind != ports.ChatEventActivityStarted {
		t.Fatalf("kind = %q", started.Kind)
	}
	if started.ActivityKind != domain.ActivityKindAutoReview {
		t.Fatalf("activity kind = %q, want %q", started.ActivityKind, domain.ActivityKindAutoReview)
	}
	if started.ActivityStatus != domain.ActivityStatusRunning {
		t.Fatalf("status = %q", started.ActivityStatus)
	}
	if started.ProviderItemID != "ao-review-5b95c779" {
		t.Fatalf("item id = %q", started.ProviderItemID)
	}
	if started.Summary != "Reviewing curl -s https://example.com" {
		t.Fatalf("summary = %q", started.Summary)
	}

	completed := normalizeOne(t, codexproto.MethodItemAutoApprovalReviewCompleted,
		`{"threadId":"019fc434","turnId":"019fc434-39c6","startedAtMs":1785703203778,"completedAtMs":1785703208165,"reviewId":"5b95c779","targetItemId":"exec-de86ce2c","decisionSource":"agent","review":{"status":"approved","riskLevel":"low","userAuthorization":"unknown","rationale":"Auto-review returned a low-risk allow decision."},"action":{"type":"command","source":"unifiedExec","command":"/bin/zsh -lc 'curl -s https://example.com'","cwd":"/ws"}}`)

	if completed.ProviderItemID != started.ProviderItemID {
		t.Fatalf("completed keyed on %q, started on %q", completed.ProviderItemID, started.ProviderItemID)
	}
	if completed.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("status = %q", completed.ActivityStatus)
	}
	if completed.Summary != "Auto-approved curl -s https://example.com (low risk)" {
		t.Fatalf("summary = %q", completed.Summary)
	}

	detail := decodeDetailMap(t, completed.Detail)
	for key, want := range map[string]string{
		"reviewId":          "5b95c779",
		"actionType":        "command",
		"status":            "approved",
		"targetItemId":      "exec-de86ce2c",
		"riskLevel":         "low",
		"userAuthorization": "unknown",
		"rationale":         "Auto-review returned a low-risk allow decision.",
		"decisionSource":    "agent",
		"command":           "curl -s https://example.com",
		"cwd":               "/ws",
		"commandSource":     "unifiedExec",
	} {
		if detail[key] != want {
			t.Errorf("detail[%q] = %q, want %q", key, detail[key], want)
		}
	}
}

func TestNormalizeAutoApprovalNonApprovalIsFailed(t *testing.T) {
	for _, status := range []string{"denied", "timedOut", "aborted"} {
		ev := normalizeOne(t, codexproto.MethodItemAutoApprovalReviewCompleted,
			`{"threadId":"th","turnId":"tu","reviewId":"r","startedAtMs":1,"completedAtMs":2,"decisionSource":"agent","review":{"status":"`+status+`"},"action":{"type":"command","command":"rm -rf /"}}`)
		if ev.ActivityStatus != domain.ActivityStatusFailed {
			t.Errorf("%s -> status %q, want failed", status, ev.ActivityStatus)
		}
	}
}

func TestNormalizeModelRerouted(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodModelRerouted,
		`{"threadId":"th","turnId":"tu","fromModel":"gpt-5.6-sol","toModel":"gpt-5.6-safety","reason":"highRiskCyberActivity"}`)
	if ev.Kind != ports.ChatEventModelRerouted {
		t.Fatalf("kind = %q", ev.Kind)
	}
	if ev.Reroute == nil {
		t.Fatal("no reroute payload")
	}
	if ev.Reroute.FromModel != "gpt-5.6-sol" || ev.Reroute.ToModel != "gpt-5.6-safety" {
		t.Fatalf("reroute = %+v", ev.Reroute)
	}
	if ev.Reroute.Reason != "highRiskCyberActivity" {
		t.Fatalf("reason = %q", ev.Reroute.Reason)
	}
	if ev.ProviderTurnID != "tu" {
		t.Fatalf("turn id = %q: a reroute belongs to the turn it happened on", ev.ProviderTurnID)
	}

	normalizeNone(t, codexproto.MethodModelRerouted, `{"threadId":"th","turnId":"tu","fromModel":"a"}`)
}

func TestNormalizeAccountUpdated(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodAccountUpdated, `{"authMode":"chatgpt","planType":"pro"}`)
	if ev.Kind != ports.ChatEventAccountChanged {
		t.Fatalf("kind = %q", ev.Kind)
	}
	if ev.Account.AuthMode != "chatgpt" || ev.Account.PlanLabel != "pro" {
		t.Fatalf("account = %+v", ev.Account)
	}
	if ev.Account.ReauthRequired {
		t.Error("an account update is not a demand for credentials")
	}

	normalizeNone(t, codexproto.MethodAccountUpdated, `{}`)
}

func TestNormalizeThreadStatusChanged(t *testing.T) {
	active := normalizeOne(t, codexproto.MethodThreadStatusChanged,
		`{"threadId":"019fc432","status":{"type":"active","activeFlags":[]}}`)
	if active.Kind != ports.ChatEventThreadState {
		t.Fatalf("kind = %q", active.Kind)
	}
	if active.ThreadState.Status != domain.ThreadStatusActive {
		t.Fatalf("status = %q", active.ThreadState.Status)
	}

	idle := normalizeOne(t, codexproto.MethodThreadStatusChanged,
		`{"threadId":"019fc432","status":{"type":"idle"}}`)
	if idle.ThreadState.Status != domain.ThreadStatusIdle {
		t.Fatalf("status = %q", idle.ThreadState.Status)
	}

	blocked := normalizeOne(t, codexproto.MethodThreadStatusChanged,
		`{"threadId":"th","status":{"type":"active","activeFlags":["waitingOnApproval"]}}`)
	if len(blocked.ThreadState.WaitingOn) != 1 || blocked.ThreadState.WaitingOn[0] != "waiting_on_approval" {
		t.Fatalf("waiting on = %+v", blocked.ThreadState.WaitingOn)
	}

	normalizeNone(t, codexproto.MethodThreadStatusChanged,
		`{"threadId":"th","status":{"type":"someFutureStatus"}}`)
}

func TestNormalizeThreadArchiveIsTriState(t *testing.T) {
	archived := normalizeOne(t, codexproto.MethodThreadArchived, `{"threadId":"019fc430"}`)
	if archived.ThreadState.Archived == nil || !*archived.ThreadState.Archived {
		t.Fatalf("archived -> %+v", archived.ThreadState)
	}

	unarchived := normalizeOne(t, codexproto.MethodThreadUnarchived, `{"threadId":"019fc430"}`)
	if unarchived.ThreadState.Archived == nil || *unarchived.ThreadState.Archived {
		t.Fatalf("unarchived -> %+v", unarchived.ThreadState)
	}

	status := normalizeOne(t, codexproto.MethodThreadStatusChanged,
		`{"threadId":"th","status":{"type":"idle"}}`)
	if status.ThreadState.Archived != nil {
		t.Fatal("a status report claimed to know whether the thread is archived")
	}
}

func TestNormalizeThreadClosed(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodThreadClosed, `{"threadId":"th"}`)
	if !ev.ThreadState.Closed || ev.ThreadState.Status != domain.ThreadStatusClosed {
		t.Fatalf("closed -> %+v", ev.ThreadState)
	}
	if ev.Kind == ports.ChatEventControllerState {
		t.Fatal("a closed thread must not be reported as the controller dying")
	}
}

func TestNormalizeNewMethodsTolerateMalformedParams(t *testing.T) {
	for _, method := range []string{
		codexproto.MethodItemReasoningSummaryTextDelta,
		codexproto.MethodItemReasoningSummaryPartAdded,
		codexproto.MethodItemReasoningTextDelta,
		codexproto.MethodTurnPlanUpdated,
		codexproto.MethodItemPlanDelta,
		codexproto.MethodItemFileChangePatchUpdated,
		codexproto.MethodItemFileChangeOutputDelta,
		codexproto.MethodItemMcpToolCallProgress,
		codexproto.MethodMcpServerStartupStatusUpdated,
		codexproto.MethodItemCommandExecutionTerminalInteraction,
		codexproto.MethodItemAutoApprovalReviewStarted,
		codexproto.MethodItemAutoApprovalReviewCompleted,
		codexproto.MethodModelRerouted,
		codexproto.MethodAccountUpdated,
		codexproto.MethodThreadStatusChanged,
	} {
		t.Run(method, func(t *testing.T) { normalizeNone(t, method, `"not an object"`) })
	}
}

func TestHandledMethodsAreDeclaredByTheProvider(t *testing.T) {
	for _, method := range []string{
		codexproto.MethodItemReasoningSummaryTextDelta,
		codexproto.MethodItemReasoningSummaryPartAdded,
		codexproto.MethodItemReasoningTextDelta,
		codexproto.MethodTurnPlanUpdated,
		codexproto.MethodItemPlanDelta,
		codexproto.MethodItemFileChangePatchUpdated,
		codexproto.MethodItemFileChangeOutputDelta,
		codexproto.MethodItemMcpToolCallProgress,
		codexproto.MethodMcpServerStartupStatusUpdated,
		codexproto.MethodConfigMcpServerReload,
		codexproto.MethodMcpServerStatusList,
		codexproto.MethodItemCommandExecutionTerminalInteraction,
		codexproto.MethodItemAutoApprovalReviewStarted,
		codexproto.MethodItemAutoApprovalReviewCompleted,
		codexproto.MethodModelRerouted,
		codexproto.MethodAccountUpdated,
		codexproto.MethodAccountChatgptAuthTokensRefresh,
		codexproto.MethodItemToolCall,
		codexproto.MethodThreadStatusChanged,
		codexproto.MethodThreadArchived,
		codexproto.MethodThreadUnarchived,
		codexproto.MethodThreadClosed,
	} {
		if !codexproto.Declares(method) {
			t.Errorf("%s is handled but not declared by %s", method, codexproto.ProviderVersion)
		}
	}
}

func decodeDetailMap(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("detail is not an object: %v (%s)", err, raw)
	}
	out := map[string]string{}
	for key, value := range decoded {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case float64:
			out[key] = strings.TrimSuffix(strings.TrimRight(formatFloat(typed), "0"), ".")
		}
	}
	return out
}

func formatFloat(value float64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func decodeDetailAny(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("detail is not an object: %v (%s)", err, raw)
	}
	return out
}

func decodeFiles(t *testing.T, raw []byte) []fileChangeDetail {
	t.Helper()
	var detail struct {
		Files []fileChangeDetail `json:"files"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("detail is not an object: %v (%s)", err, raw)
	}
	return detail.Files
}
