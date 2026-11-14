package codexappserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
)

var driverMethods = []struct {
	method  string
	payload any
}{
	{codexproto.MethodInitialize, &codexproto.InitializeParams{}},
	{codexproto.MethodThreadStart, &codexproto.ThreadStartParams{}},
	{codexproto.MethodThreadResume, &codexproto.ThreadResumeParams{}},

	{codexproto.MethodTurnStart, &codexproto.TurnStartParams{}},
	{codexproto.MethodTurnInterrupt, &codexproto.TurnInterruptParams{}},

	{codexproto.MethodModelList, &codexproto.ModelListParams{}},
	{codexproto.MethodThreadCompactStart, &codexproto.ThreadCompactStartParams{}},
	{codexproto.MethodAccountRateLimitsRead, nil},
	{codexproto.MethodSkillsList, &codexproto.SkillsListParams{}},
	{codexproto.MethodConfigMcpServerReload, nil},
	{codexproto.MethodMcpServerStatusList, &codexproto.ListMcpServerStatusParams{}},

	{codexproto.MethodTurnStarted, nil},
	{codexproto.MethodTurnCompleted, nil},
	{codexproto.MethodItemStarted, nil},
	{codexproto.MethodItemCompleted, nil},
	{codexproto.MethodItemAgentMessageDelta, nil},
	{codexproto.MethodItemCommandExecutionOutputDelta, nil},
	{codexproto.MethodTurnDiffUpdated, nil},
	{codexproto.MethodThreadTokenUsageUpdated, nil},
	{codexproto.MethodAccountRateLimitsUpdated, nil},

	{codexproto.MethodItemReasoningSummaryTextDelta, &codexproto.ReasoningSummaryTextDeltaNotification{}},
	{codexproto.MethodItemReasoningSummaryPartAdded, &codexproto.ReasoningSummaryPartAddedNotification{}},
	{codexproto.MethodItemReasoningTextDelta, &codexproto.ReasoningTextDeltaNotification{}},

	{codexproto.MethodTurnPlanUpdated, &codexproto.TurnPlanUpdatedNotification{}},
	{codexproto.MethodItemPlanDelta, &codexproto.PlanDeltaNotification{}},

	{codexproto.MethodItemFileChangePatchUpdated, &codexproto.FileChangePatchUpdatedNotification{}},
	{codexproto.MethodItemFileChangeOutputDelta, &codexproto.FileChangeOutputDeltaNotification{}},

	{codexproto.MethodMcpServerStartupStatusUpdated, &codexproto.McpServerStatusUpdatedNotification{}},
	{codexproto.MethodItemMcpToolCallProgress, &codexproto.McpToolCallProgressNotification{}},

	{codexproto.MethodItemCommandExecutionTerminalInteraction, &codexproto.TerminalInteractionNotification{}},

	{codexproto.MethodItemAutoApprovalReviewStarted, &codexproto.ItemGuardianApprovalReviewStartedNotification{}},
	{codexproto.MethodItemAutoApprovalReviewCompleted, &codexproto.ItemGuardianApprovalReviewCompletedNotification{}},

	{codexproto.MethodModelRerouted, &codexproto.ModelReroutedNotification{}},
	{codexproto.MethodAccountUpdated, &codexproto.AccountUpdatedNotification{}},

	{codexproto.MethodThreadStatusChanged, &codexproto.ThreadStatusChangedNotification{}},
	{codexproto.MethodThreadArchived, &codexproto.ThreadArchivedNotification{}},
	{codexproto.MethodThreadUnarchived, &codexproto.ThreadUnarchivedNotification{}},
	{codexproto.MethodThreadClosed, &codexproto.ThreadClosedNotification{}},

	{codexproto.MethodAccountChatgptAuthTokensRefresh, &codexproto.ChatgptAuthTokensRefreshParams{}},
	{codexproto.MethodItemToolCall, &codexproto.DynamicToolCallParams{}},

	{codexproto.MethodItemCommandExecutionRequestApproval, nil},
	{codexproto.MethodItemFileChangeRequestApproval, nil},
	{codexproto.MethodItemPermissionsRequestApproval, nil},
	{codexproto.MethodItemToolRequestUserInput, nil},
}

func TestEveryMethodTheDriverUsesIsDeclaredByTheProvider(t *testing.T) {
	for _, m := range driverMethods {
		if !codexproto.Declares(m.method) {
			t.Errorf("driver uses %q, which the generated protocol (%s) does not declare",
				m.method, codexproto.ProviderVersion)
		}
	}
}

func TestApprovalKindsAreExhaustive(t *testing.T) {
	var approvals []string
	for _, m := range codexproto.Methods {
		if strings.HasSuffix(m.Name, "/requestApproval") {
			approvals = append(approvals, m.Name)
		}
	}
	handled := map[string]bool{
		codexproto.MethodItemCommandExecutionRequestApproval: true,
		codexproto.MethodItemFileChangeRequestApproval:       true,
		codexproto.MethodItemPermissionsRequestApproval:      true,
	}
	for _, name := range approvals {
		if !handled[name] {
			t.Errorf("provider declares approval %q, which the driver does not handle: "+
				"a turn blocked on it would wait forever", name)
		}
	}
	if len(approvals) != len(handled) {
		t.Errorf("provider declares %d approval kinds, driver handles %d", len(approvals), len(handled))
	}
}

func TestGeneratedProtocolMatchesTheInstalledProvider(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed; nothing to compare the generated protocol against")
	}

	dir := t.TempDir()
	out, err := exec.Command(bin, "app-server", "generate-json-schema", "--out", dir).CombinedOutput()
	if err != nil {
		t.Skipf("provider declined to emit its schema (%v): %s", err, out)
	}

	live := methodsFromSchema(t, dir)
	generated := map[string]bool{}
	for _, m := range codexproto.Methods {
		generated[m.Name] = true
	}

	var missing, extra []string
	for name := range live {
		if !generated[name] {
			missing = append(missing, name)
		}
	}
	for name := range generated {
		if !live[name] {
			if name == "thread/rollback" && live["thread/revert"] {
				continue
			}
			extra = append(extra, name)
		}
	}

	if len(missing) > 0 {
		t.Logf("installed provider declares %d method(s) the generated file lacks; "+
			"run `go generate ./internal/adapters/chatdriver/codexappserver/...` to pick them up: %v",
			len(missing), truncate(missing))
	}
	if len(extra) > 0 {
		t.Errorf("generated protocol declares %d method(s) the installed provider does not: %v",
			len(extra), truncate(extra))
	}
}

func methodsFromSchema(t *testing.T, dir string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for name := range methodDirectionsFromSchema(t, dir) {
		found[name] = true
	}
	return found
}

func methodDirectionsFromSchema(t *testing.T, dir string) map[string]string {
	t.Helper()
	found := map[string]string{}
	for _, file := range []string{
		"ClientRequest.json", "ClientNotification.json",
		"ServerRequest.json", "ServerNotification.json",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			continue
		}
		var doc struct {
			OneOf []struct {
				Properties struct {
					Method struct {
						Enum []string `json:"enum"`
					} `json:"method"`
				} `json:"properties"`
			} `json:"oneOf"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		direction := strings.TrimSuffix(file, ".json")
		for _, arm := range doc.OneOf {
			for _, name := range arm.Properties.Method.Enum {
				found[name] = direction
			}
		}
	}
	if len(found) == 0 {
		t.Fatalf("no methods parsed out of %s", dir)
	}
	return found
}

func truncate(names []string) []string {
	const limit = 12
	if len(names) <= limit {
		return names
	}
	return append(names[:limit:limit], "…")
}

func TestRealtimeStaysUnreachableUntilTheProviderPublishesAnEntryPoint(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed; nothing to check the realtime surface against")
	}

	dir := t.TempDir()
	if out, err := exec.Command(bin, "app-server", "generate-json-schema", "--out", dir).CombinedOutput(); err != nil {
		t.Skipf("provider declined to emit its schema (%v): %s", err, out)
	}

	var startable []string
	for method, direction := range methodDirectionsFromSchema(t, dir) {
		if !strings.Contains(method, "realtime") {
			continue
		}
		if strings.HasPrefix(direction, "Client") {
			startable = append(startable, method+" ("+direction+")")
		}
	}
	sort.Strings(startable)

	if len(startable) > 0 {
		t.Errorf("the provider now lets a client start a realtime session: %v\n"+
			"Voice is buildable — see this test's comment for what that entails. "+
			"Regenerate the protocol and decide whether to take it on; if not, "+
			"record the decision here so this stops reporting it.", startable)
	}
}
