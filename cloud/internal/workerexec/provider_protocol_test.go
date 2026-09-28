package workerexec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	acp "github.com/coder/acp-go-sdk"
)

func TestCodexSteerWaitsForProviderAcknowledgement(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	defer requestReader.Close()
	defer requestWriter.Close()
	defer responseReader.Close()
	defer responseWriter.Close()
	connection := newCodexRPC(requestWriter, responseReader)
	session := &codexSession{conn: connection, threadID: "thread-1", turnID: "cloud-turn", providerTurnID: "provider-turn"}
	served := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(requestReader).ReadBytes('\n')
		if err != nil {
			served <- err
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				ThreadID       string `json:"threadId"`
				ExpectedTurnID string `json:"expectedTurnId"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &request); err != nil {
			served <- err
			return
		}
		if request.Method != "turn/steer" || request.Params.ThreadID != "thread-1" || request.Params.ExpectedTurnID != "provider-turn" {
			served <- errors.New("steer did not target the active provider turn")
			return
		}
		_, err = responseWriter.Write([]byte(`{"id":1,"result":{"turnId":"provider-turn"}}` + "\n"))
		served <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Steer(ctx, "cloud-turn", "change course"); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestACPDoesNotDiscardCommandDenyRules(t *testing.T) {
	s := &Supervisor{}
	err := s.runACP(context.Background(), worker.Turn{Harness: "claude-code", DeniedCommands: []string{"git push"}}, Command{}, nil, nil)
	if !errors.Is(err, ErrUnsupportedPolicy) {
		t.Fatalf("ACP deny rule = %v, want fail closed", err)
	}
}

func TestCodexAppServerApprovalSettingsMatchLocal(t *testing.T) {
	for _, test := range []struct{ mode, approval, policy, sandbox, reviewer string }{
		{"trusted", "default", "never", "danger-full-access", "user"},
		{"trusted", "bypass-permissions", "never", "danger-full-access", "user"},
		{"standard", "accept-edits", "on-request", "workspace-write", "user"},
		{"standard", "auto", "on-request", "workspace-write", "auto_review"},
		{"read-only", "", "never", "read-only", "user"},
	} {
		policy, sandbox, reviewer := codexApprovalSettings(worker.Turn{Mode: test.mode, ApprovalMode: test.approval})
		if policy != test.policy || sandbox != test.sandbox || reviewer != test.reviewer {
			t.Fatalf("%s/%s = %s/%s/%s", test.mode, test.approval, policy, sandbox, reviewer)
		}
	}
}

func TestCursorACPAcceptEditsOnlyApprovesFileChanges(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionId: "allow", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
	}
	for _, test := range []struct {
		kind    acp.ToolKind
		approve bool
	}{
		{acp.ToolKindEdit, true}, {acp.ToolKindDelete, true}, {acp.ToolKindMove, true}, {acp.ToolKindExecute, false},
	} {
		kind := test.kind
		option, approved := automaticACPDecision(worker.Turn{Harness: "cursor", ApprovalMode: "accept-edits"}, acp.RequestPermissionRequest{
			ToolCall: acp.ToolCallUpdate{Kind: &kind}, Options: options,
		})
		if approved != test.approve || (approved && option != "allow") {
			t.Fatalf("%s = %s/%v", kind, option, approved)
		}
	}
}

func TestACPSteeringRequiresProviderAdvertisement(t *testing.T) {
	if acpSteeringSupported(nil) || acpSteeringSupported(map[string]any{"steering": map[string]any{"supported": false}}) {
		t.Fatal("advertisement was absent")
	}
	if !acpSteeringSupported(map[string]any{"steering": map[string]any{"supported": true}}) {
		t.Fatal("advertised steering was hidden")
	}
}

func TestACPSettingsUseAdvertisedChoices(t *testing.T) {
	choices := acp.SessionConfigSelectOptionsUngrouped{{Value: "sonnet"}, {Value: "opus"}}
	options := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
		Id: "model", Options: acp.SessionConfigSelectOptions{Ungrouped: &choices},
	}}}
	if !acpOptionOffered(options, "model", "opus") || acpOptionOffered(options, "model", "unlisted") {
		t.Fatal("ACP model choices were not validated against the provider catalog")
	}
	if acpModeOffered(options, nil, "auto") {
		t.Fatal("unadvertised ACP approval mode was accepted")
	}
	modes := &acp.SessionModeState{AvailableModes: []acp.SessionMode{{Id: "default"}, {Id: "auto"}}}
	if !acpModeOffered(options, modes, "auto") {
		t.Fatal("advertised ACP approval mode was hidden")
	}
}
