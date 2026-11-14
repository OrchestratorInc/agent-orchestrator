package shellterm

import (
	"reflect"
	"testing"
)

func TestCommandTerminalHandlesReconcilesOwnership(t *testing.T) {
	store := &fakeShellTerminalStore{records: []ShellTerminalRecord{
		{HandleID: "codex", AppRunID: testAppRunID, Transient: true, CommandOwner: "agentauth:codex", Title: "Renamed"},
		{HandleID: "legacy", AppRunID: testAppRunID, Transient: true},
		{HandleID: "claude", AppRunID: testAppRunID, Transient: true, CommandOwner: "agentauth:claude-code"},
		{HandleID: "github", AppRunID: testAppRunID, Transient: true, CommandOwner: "github-auth"},
		{HandleID: "managed-codex", AppRunID: testAppRunID, Transient: true, CommandOwner: "codex-account-auth"},
		{HandleID: "user-shell", AppRunID: testAppRunID, Title: "Log in to Codex"},
		{HandleID: "old-app-login", AppRunID: "previous-app", Transient: true, CommandOwner: "agentauth:codex"},
		{HandleID: "old-app-legacy", AppRunID: "previous-app", Transient: true},
	}}
	svc := newTestService(newFakeShellRuntime(), store, nil)
	handles, err := svc.CommandTerminalHandles(t.Context(), "agentauth:codex")
	if err != nil || !reflect.DeepEqual(handles, []string{"codex", "legacy"}) {
		t.Fatalf("retained command handles = %v, %v; want codex and legacy", handles, err)
	}
}
