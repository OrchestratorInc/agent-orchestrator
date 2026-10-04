package persistenthost

import (
	"bufio"
	"context"
	"os"
	"testing"
)

func TestCodexStateCapturesOfficialThreadSettings(t *testing.T) {
	h := &host{}
	h.observeCodexClientFrameLocked([]byte(`{"id":1,"method":"thread/start"}`))
	h.observeCodexFrameLocked([]byte(`{"id":1,"result":{"thread":{"id":"actual"},"model":"configured","reasoningEffort":"medium"}}`))
	if h.codex == nil || h.codex.ThreadID != "actual" || h.codex.Model != "configured" || h.codex.Effort != "medium" {
		t.Fatalf("state = %#v", h.codex)
	}
	h.observeCodexFrameLocked([]byte(`{"method":"thread/settings/updated","params":{"threadId":"other","threadSettings":{"model":"wrong","effort":"low"}}}`))
	if h.codex.Model != "configured" {
		t.Fatal("another thread changed baseline")
	}
	h.observeCodexFrameLocked([]byte(`{"method":"thread/settings/updated","params":{"threadId":"actual","threadSettings":{"model":"chosen","effort":"high"}}}`))
	if h.codex.Model != "chosen" || h.codex.Effort != "high" {
		t.Fatalf("updated state = %#v", h.codex)
	}
	h.observeCodexFrameLocked([]byte(`{"method":"thread/settings/updated","params":{"threadId":"actual","threadSettings":{"model":"chosen","effort":null}}}`))
	if h.codex.Effort != "" {
		t.Fatal("null effort did not clear reported effort")
	}
}

func TestCodexStateIgnoresUncorrelatedAndFailedResponses(t *testing.T) {
	h := &host{}
	h.observeCodexFrameLocked([]byte(`{"id":1,"result":{"thread":{"id":"wrong"},"model":"wrong"}}`))
	if h.codex != nil {
		t.Fatal("uncorrelated response created state")
	}
	h.observeCodexClientFrameLocked([]byte(`{"id":2,"method":"thread/resume"}`))
	h.observeCodexFrameLocked([]byte(`{"id":2,"error":{"code":-1}}`))
	if h.codex != nil || h.codexRequestID != "" {
		t.Fatal("failed resume retained settings or pending request")
	}
}

func TestHostReconnectCarriesCodexSettingsWithoutReplayingStart(t *testing.T) {
	cfg := Config{SessionID: "codex-settings", DataDir: t.TempDir(), Workdir: t.TempDir(), Env: append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1", "AO_CHAT_HOST_CODEX_SETTINGS_HELPER=1"), Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}}
	hostDone := make(chan error, 1)
	go func() { hostDone <- Run(context.Background(), cfg) }()
	d := awaitDescriptor(t, cfg.DataDir, cfg.SessionID)
	defer func() {
		if err := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
			t.Error(err)
		}
		if err := <-hostDone; err != nil {
			t.Error(err)
		}
	}()
	first, err := attach(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	sendFrame(t, first, `{"id":1,"method":"thread/start"}`)
	_ = readFrame(t, bufio.NewReader(first.Stdout))
	_ = first.Stdin.Close()
	second := awaitAttach(t, d)
	defer func() { _ = second.Stdin.Close() }()
	if second.CodexState == nil || second.CodexState.ThreadID != "actual" || second.CodexState.Model != "configured" || second.CodexState.Effort != "medium" {
		t.Fatalf("reconnected state = %#v", second.CodexState)
	}
}
