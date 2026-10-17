package acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func standbyFixture(t *testing.T) (Config, ports.ChatStartConfig, string) {
	t.Helper()
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	cfg, start := restartFixture(t, map[string]string{"AO_TEST_PERSISTENT_ACP_CALLS": callsPath})
	cfg.Standby = &StandbyConfig{Warm: func(ctx context.Context, request RawRequest) error {
		return request(ctx, "cursor/list_available_models", map[string]any{})
	}}
	start.Env = map[string]string{"AO_TEST_ENV": "1"}
	start.SystemPrompt = "standing"
	t.Cleanup(func() {
		_ = persistenthost.Shutdown(context.Background(), start.DataDir, string(start.SessionID))
	})
	return cfg, start, callsPath
}

func providerCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider calls: %v", err)
	}
	return strings.Fields(string(data))
}

func countCalls(calls []string, method string) int {
	n := 0
	for _, call := range calls {
		if call == method {
			n++
		}
	}
	return n
}

func hostDescriptorExists(dataDir, key string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "chat-hosts", key, "host.json"))
	return err == nil
}

func TestStartAdoptsMatchingStandby(t *testing.T) {
	cfg, start, callsPath := standbyFixture(t)
	driver := New(cfg, nil)
	prewarm := start
	prewarm.ProviderScopeID = ""
	standby, err := driver.Prewarm(context.Background(), prewarm)
	if err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	standbyKey := standbyKeyOf(standby)
	if standbyKey == string(start.SessionID) || !hostDescriptorExists(start.DataDir, standbyKey) {
		t.Fatal("standby host is not under its own standby key")
	}

	start.Standby = standby
	start.SystemPrompt = "standing\n\n<ao-continuation switch-id=s1>handoff</ao-continuation>"
	conv, err := driver.Start(context.Background(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()

	calls := providerCalls(t, callsPath)
	if got := countCalls(calls, "initialize"); got != 1 {
		t.Fatalf("initialize calls = %d (%v), want the standby's only", got, calls)
	}
	if countCalls(calls, "cursor/list_available_models") != 1 || countCalls(calls, "session/new") != 1 {
		t.Fatalf("provider calls = %v, want one warm-up and one session/new", calls)
	}
	if hostDescriptorExists(start.DataDir, standbyKey) {
		t.Fatal("standby key still published after adoption")
	}
	if !hostDescriptorExists(start.DataDir, string(start.SessionID)) {
		t.Fatal("adopted host is not under the session key")
	}
	if got := conv.(*conversation).providerScopeID; got != start.ProviderScopeID {
		t.Fatalf("provider scope = %q, want the adopting start's %q", got, start.ProviderScopeID)
	}
	// Adoption is final: a late Discard must not stop the adopted provider.
	standby.Discard()
	_ = nextEvent(t, conv.Events())
	completedProviderPID(t, conv)
}

func TestStartDiscardsStandbyWithChangedInputs(t *testing.T) {
	cfg, start, callsPath := standbyFixture(t)
	driver := New(cfg, nil)
	standby, err := driver.Prewarm(context.Background(), start)
	if err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	standbyKey := standbyKeyOf(standby)
	start.Standby = standby
	start.Model = "another-model"
	conv, err := driver.Start(context.Background(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()
	if got := countCalls(providerCalls(t, callsPath), "initialize"); got != 2 {
		t.Fatalf("initialize calls = %d, want a fresh provider after the mismatch", got)
	}
	waitHostGone(t, start.DataDir, standbyKey)
}

func TestStandbyDiscardStopsItsHost(t *testing.T) {
	cfg, start, _ := standbyFixture(t)
	standby, err := New(cfg, nil).Prewarm(context.Background(), start)
	if err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	standbyKey := standbyKeyOf(standby)
	standby.Discard()
	standby.Discard()
	waitHostGone(t, start.DataDir, standbyKey)
}

func TestPrewarmRequiresStandbySupport(t *testing.T) {
	cfg, start, _ := standbyFixture(t)
	cfg.Standby = nil
	if _, err := New(cfg, nil).Prewarm(context.Background(), start); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("Prewarm without standby support = %v, want ErrChatUnsupported", err)
	}
}

func TestContinuationPreambleExtractsHandoffBlock(t *testing.T) {
	blocks := ContinuationPreamble("standing\n<ao-continuation kind=\"switch\">facts</ao-continuation>\n")
	if len(blocks) != 1 || blocks[0].Text == nil || blocks[0].Text.Text != `<ao-continuation kind="switch">facts</ao-continuation>` {
		t.Fatalf("preamble = %#v", blocks)
	}
	if ContinuationPreamble("standing only") != nil {
		t.Fatal("a prompt without a handoff produced a preamble")
	}
	// The continuation protocol names the bare tag in prose ahead of the block.
	withProtocol := "AO places context inside an <ao-continuation> block.\n\n<ao-continuation switch-id=s1>facts</ao-continuation>"
	blocks = ContinuationPreamble(withProtocol)
	if len(blocks) != 1 || blocks[0].Text.Text != "<ao-continuation switch-id=s1>facts</ao-continuation>" {
		t.Fatalf("preamble with protocol prose = %#v, want only the block", blocks)
	}
}

func TestSecondStandbyOutlivesTheAdoptedHost(t *testing.T) {
	cfg, start, callsPath := standbyFixture(t)
	driver := New(cfg, nil)
	first, err := driver.Prewarm(context.Background(), start)
	if err != nil {
		t.Fatalf("first Prewarm: %v", err)
	}
	firstKey := standbyKeyOf(first)
	adoptCfg := start
	adoptCfg.Standby = first
	adopted, err := driver.Start(context.Background(), adoptCfg)
	if err != nil {
		t.Fatalf("adopt first: %v", err)
	}

	// The user picks another agent for the next switch while the adopted one
	// still owns the session.
	second, err := driver.Prewarm(context.Background(), start)
	if err != nil {
		t.Fatalf("second Prewarm: %v", err)
	}
	secondKey := standbyKeyOf(second)
	if secondKey == firstKey {
		t.Fatalf("second standby reused the adopted host's launch key %q", firstKey)
	}
	// The switch stops the adopted host; its exit must leave the new standby.
	if err := adopted.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatalf("terminate adopted: %v", err)
	}
	if !hostDescriptorExists(start.DataDir, secondKey) {
		t.Fatal("stopping the adopted host removed the second standby")
	}
	adoptCfg.Standby = second
	conv, err := driver.Start(context.Background(), adoptCfg)
	if err != nil {
		t.Fatalf("adopt second: %v", err)
	}
	defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()
	if got := countCalls(providerCalls(t, callsPath), "initialize"); got != 2 {
		t.Fatalf("initialize calls = %d, want one per standby and no cold start", got)
	}
}

func standbyKeyOf(handle ports.ChatStandby) string { return handle.(*standby).key.get() }

func waitHostGone(t *testing.T, dataDir, key string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for hostDescriptorExists(dataDir, key) {
		if time.Now().After(deadline) {
			t.Fatalf("host %s still running", key)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStandbyIsNotAdoptedWhenInstructionsGainASection(t *testing.T) {
	cfg, start, callsPath := standbyFixture(t)
	driver := New(cfg, nil)
	handle, err := driver.Prewarm(context.Background(), start)
	if err != nil {
		t.Fatalf("Prewarm: %v", err)
	}
	start.Standby = handle
	// An extra section the standby never saw (say, artifact output) must not be
	// dropped by adopting a process launched without it.
	start.SystemPrompt = "standing\n\nWrite the deliverable as an artifact."
	conv, err := driver.Start(context.Background(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()
	if got := countCalls(providerCalls(t, callsPath), "initialize"); got != 2 {
		t.Fatalf("initialize calls = %d, want a fresh provider for the changed instructions", got)
	}
}
