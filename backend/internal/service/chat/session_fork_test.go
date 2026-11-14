package chat

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type forkTestConv struct {
	ports.ChatConversation
	id     string
	closed bool
}

func (c *forkTestConv) ProviderConversationID() string { return c.id }
func (c *forkTestConv) Close() error                   { c.closed = true; return nil }

type terminatingForkConv struct {
	forkTestConv
	terminated bool
}

func (c *terminatingForkConv) Terminate() error { c.terminated = true; return nil }

type terminatingForkDriver struct {
	ports.ChatDriver
	conv *terminatingForkConv
}

func (d *terminatingForkDriver) ForkIntoHost(context.Context, string, string, ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.conv = &terminatingForkConv{forkTestConv: forkTestConv{id: "child"}}
	return d.conv, nil
}

func TestForkHostIsTerminated(t *testing.T) {
	driver := &terminatingForkDriver{}
	plan := sessionForkPlan{driver: driver, sourceProviderID: "s", nativeAnchor: "a"}
	if _, err := plan.prepare(context.Background(), ports.ForkTarget{SessionID: "f", WorkspacePath: "/w"}); err != nil {
		t.Fatal(err)
	}
	if !driver.conv.terminated {
		t.Fatal("a detached temporary host would keep running")
	}
}

type isolatedForkDriver struct {
	ports.ChatDriver
	anchorRequired bool
	gotSource      string
	gotAnchor      string
	gotCfg         ports.ChatStartConfig
	calls          int
	conv           *forkTestConv
}

func (d *isolatedForkDriver) ForkIntoHost(_ context.Context, source, anchor string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.calls++
	if d.anchorRequired != (anchor != "") {
		return nil, ports.ErrChatCapabilityUnavailable
	}
	d.gotSource, d.gotAnchor, d.gotCfg = source, anchor, cfg
	d.conv = &forkTestConv{id: "child"}
	return d.conv, nil
}

type workspaceForkDriver struct {
	ports.ChatDriver
	gotSourceWorkspace string
	gotUpTo            string
	gotCfg             ports.ChatStartConfig
}

func (d *workspaceForkDriver) ForkIntoWorkspace(_ context.Context, source, sourceWorkspace, upTo string, cfg ports.ChatStartConfig) (string, error) {
	d.gotUpTo = upTo
	d.gotSourceWorkspace, d.gotCfg = sourceWorkspace, cfg
	return "claude-child", nil
}

type plainDriver struct{ ports.ChatDriver }

func TestLatestForkTurnSkipsUnfinishedAndRolledBack(t *testing.T) {
	now := time.Now()
	rows := ConversationRows{Turns: []domain.ConversationTurn{
		{ID: "t1", State: domain.TurnStateCompleted, ProviderTurnID: "p1"},
		{ID: "t2", State: domain.TurnStateCompleted, ProviderTurnID: "p2"},
		{ID: "t3", State: domain.TurnStateCompleted, ProviderTurnID: "p3", RolledBackAt: &now},
		{ID: "t4", State: domain.TurnStateFailed},
	}}
	turn, ok := latestFinishedTurn(rows)
	if !ok || turn.ID != "t2" {
		t.Fatalf("turn = %+v, %v", turn, ok)
	}
	if _, ok := latestFinishedTurn(ConversationRows{}); ok {
		t.Fatal("empty conversation has no turn")
	}
}

func TestCodexNativeTurnIDStripsScope(t *testing.T) {
	for in, want := range map[string]string{
		"turn-1":              "turn-1",
		"codex:5:abcdeturn-1": "turn-1",
		"codex:3:a:bturn-9":   "turn-9",
		"codex:x:broken":      "codex:x:broken",
		"codex:99:short":      "codex:99:short",
	} {
		if got := codexNativeTurnID(in); got != want {
			t.Errorf("codexNativeTurnID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForkSupportByAgent(t *testing.T) {
	if got := sessionForkStrategy(&workspaceForkDriver{}); got != forkIntoWorkspace {
		t.Fatalf("claude = %v", got)
	}
	if got := sessionForkStrategy(&isolatedForkDriver{}); got != forkIntoHost {
		t.Fatalf("isolated = %v", got)
	}
	if got := sessionForkStrategy(plainDriver{}); got != forkUnsupported {
		t.Fatalf("plain = %v", got)
	}
}

func TestCodexForkUsesNewWorktree(t *testing.T) {
	driver := &isolatedForkDriver{anchorRequired: true}
	plan := sessionForkPlan{driver: driver, sourceProviderID: "thread-1", nativeAnchor: "turn-2", model: "m", permissions: ports.PermissionModeAuto}
	id, err := plan.prepare(context.Background(), ports.ForkTarget{SessionID: "fork-1", DataDir: "/data", WorkspacePath: "/w/fork", Env: map[string]string{"A": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if id != "child" || driver.gotSource != "thread-1" || driver.gotAnchor != "turn-2" {
		t.Fatalf("id=%q source=%q anchor=%q", id, driver.gotSource, driver.gotAnchor)
	}
	if driver.gotCfg.WorkspacePath != "/w/fork" || driver.gotCfg.SessionID == "fork-1" || driver.gotCfg.DataDir != "/data" || driver.gotCfg.Model != "m" {
		t.Fatalf("cfg = %+v", driver.gotCfg)
	}
	if !driver.conv.closed {
		t.Fatal("the temporary fork host must be closed before the fork starts")
	}
}

func TestACPForkFallsBackToCurrentContext(t *testing.T) {
	driver := &isolatedForkDriver{}
	plan := sessionForkPlan{driver: driver, sourceProviderID: "s", nativeAnchor: "turn-2"}
	if _, err := plan.prepare(context.Background(), ports.ForkTarget{SessionID: "f", WorkspacePath: "/w"}); err != nil {
		t.Fatal(err)
	}
	if driver.calls != 2 || driver.gotAnchor != "" {
		t.Fatalf("calls=%d anchor=%q", driver.calls, driver.gotAnchor)
	}
}

func TestClaudeForkUsesSourceWorkspace(t *testing.T) {
	driver := &workspaceForkDriver{}
	plan := sessionForkPlan{driver: driver, sourceProviderID: "s", sourceWorkspace: "/w/src"}
	id, err := plan.prepare(context.Background(), ports.ForkTarget{SessionID: "f", WorkspacePath: "/w/fork"})
	if err != nil || id != "claude-child" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if driver.gotSourceWorkspace != "/w/src" || driver.gotCfg.WorkspacePath != "/w/fork" {
		t.Fatalf("source=%q cfg=%+v", driver.gotSourceWorkspace, driver.gotCfg)
	}
}

func TestFailedForkReturnsError(t *testing.T) {
	plan := sessionForkPlan{driver: plainDriver{}, sourceProviderID: "s"}
	if _, err := plan.prepare(context.Background(), ports.ForkTarget{SessionID: "f", WorkspacePath: "/w"}); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestLatestForkTurnAcceptsImportedTurns(t *testing.T) {
	// Native history import marks its last turn recovered; a fork of a fork
	// must still find it.
	rows := ConversationRows{Turns: []domain.ConversationTurn{{ID: "t1", State: domain.TurnStateRecovered}}}
	if turn, ok := latestFinishedTurn(rows); !ok || turn.ID != "t1" {
		t.Fatalf("turn = %+v, %v", turn, ok)
	}
}

func TestForkNeedsANativeForkCapability(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  sessionForkMode
		caps  ports.ChatCapabilities
		known bool
		want  bool
	}{
		{"unknown capabilities stay allowed", forkIntoHost, nil, false, true},
		{"turn fork", forkIntoHost, ports.ChatCapabilities{ports.ChatCapabilityFork: true}, true, true},
		{"current-context fork", forkIntoHost, ports.ChatCapabilities{ports.ChatCapabilityForkCurrent: true}, true, true},
		{"no fork", forkIntoHost, ports.ChatCapabilities{ports.ChatCapabilityResume: true}, true, false},
		{"workspace fork ignores ACP caps", forkIntoWorkspace, ports.ChatCapabilities{}, true, true},
	} {
		if got := forkCapable(tc.mode, tc.caps, tc.known); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestOnlyASettledLatestTurnCanBeForked(t *testing.T) {
	for state, want := range map[domain.TurnState]bool{
		domain.TurnStateCompleted: true, domain.TurnStateRecovered: true, domain.TurnStateInterrupted: true,
		domain.TurnStateFailed: true, domain.TurnStateRunning: false, domain.TurnStateQueued: false,
	} {
		if got := settledForkTurn(domain.ConversationTurn{State: state}); got != want {
			t.Errorf("%s: got %v", state, got)
		}
	}
}

func TestOlderForkPointFindsTheNextTurn(t *testing.T) {
	now := time.Now()
	rows := ConversationRows{Turns: []domain.ConversationTurn{
		{ID: "t1", State: domain.TurnStateCompleted},
		{ID: "t2", State: domain.TurnStateCompleted},
		{ID: "t2b", State: domain.TurnStateCompleted, RolledBackAt: &now},
		{ID: "t3", State: domain.TurnStateFailed},
		{ID: "t4", State: domain.TurnStateCompleted},
	}}
	turn, next, ok := olderForkPoint(rows, "t2")
	if !ok || turn.ID != "t2" || next.ID != "t3" {
		t.Fatalf("turn=%q next=%q ok=%v", turn.ID, next.ID, ok)
	}
	if _, next, ok := olderForkPoint(rows, "t4"); !ok || next.ID != "" {
		t.Fatalf("the latest turn has no next turn: %q %v", next.ID, ok)
	}
	if _, _, ok := olderForkPoint(rows, "t3"); ok {
		t.Fatal("a failed turn isn't a fork point")
	}
	if _, _, ok := olderForkPoint(rows, "t2b"); ok {
		t.Fatal("a rolled-back turn isn't a fork point")
	}
}

func TestLastAssistantAPIMessageID(t *testing.T) {
	rows := ConversationRows{Messages: []domain.ConversationMessage{
		{TurnID: "t1", Role: domain.MessageRoleAssistant, ProviderItemID: "scope:msg_01AAA"},
		{TurnID: "t1", Role: domain.MessageRoleAssistant, ProviderItemID: "msg_01BBB"},
		{TurnID: "t2", Role: domain.MessageRoleAssistant, ProviderItemID: "msg_01CCC"},
		{TurnID: "t1", Role: domain.MessageRoleUser, ProviderItemID: "msg_01USER"},
	}}
	if got := lastAssistantAPIMessageID(rows, "t1"); got != "msg_01BBB" {
		t.Fatalf("got %q", got)
	}
	if got := lastAssistantAPIMessageID(rows, "t9"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// A fork of an older turn takes only the attachments its conversation had by
// then: the ones named in messages of that turn and the turns before it.
func TestAttachmentsUpToTurnLeavesOutLaterOnes(t *testing.T) {
	rows := ConversationRows{
		Turns: []domain.ConversationTurn{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}},
		Messages: []domain.ConversationMessage{
			{TurnID: "t1", Text: "look at .ao/attachments/image-aaa.png please"},
			{TurnID: "t2", Text: "and this", DeliveryContentJSON: `[{"type":"image","path":".ao/attachments/attachment-bbb.pdf"}]`},
			{TurnID: "t2", Text: "no attachment here"},
			{TurnID: "t3", Text: "later: .ao/attachments/image-ccc.png"},
		},
	}
	got := attachmentsUpToTurn(rows, "t2")
	want := []string{"attachment-bbb.pdf", "image-aaa.png"}
	if !slices.Equal(got, want) {
		t.Fatalf("attachments = %v, want %v", got, want)
	}
	if got := attachmentsUpToTurn(rows, "t1"); !slices.Equal(got, []string{"image-aaa.png"}) {
		t.Fatalf("attachments up to t1 = %v", got)
	}
}

// A Claude fork of the latest turn copies the session only up to that turn,
// so a turn the original starts while the fork is being made stays out.
func TestClaudeLatestTurnForkStopsAtThatTurn(t *testing.T) {
	driver := &workspaceForkDriver{}
	rows := ConversationRows{Messages: []domain.ConversationMessage{
		{TurnID: "t2", Role: domain.MessageRoleAssistant, ProviderItemID: "msg_LATEST"},
		{TurnID: "t3", Role: domain.MessageRoleAssistant, ProviderItemID: "msg_NEWER"},
	}}
	plan := sessionForkPlan{
		driver: driver, sourceProviderID: "s", sourceWorkspace: "/w/src",
		cutTurnID: "t2", conversationID: "c",
		reader: SnapshotReaderFunc(func(context.Context, string) (ConversationRows, error) { return rows, nil }),
	}
	if _, err := plan.prepare(context.Background(), ports.ForkTarget{SessionID: "f", WorkspacePath: "/w/fork"}); err != nil {
		t.Fatal(err)
	}
	if driver.gotUpTo != "msg_LATEST" {
		t.Fatalf("copy cut at %q, want the forked turn's last reply", driver.gotUpTo)
	}
}
