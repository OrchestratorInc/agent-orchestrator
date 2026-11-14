package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReceiveTransferRefusesMalformedPreservedRef(t *testing.T) {
	m := &Manager{dataDir: t.TempDir()}
	for _, ref := range []string{"refs/heads/main", "refs/ao/preserved/x:refs/heads/main", "--upload-pack=x"} {
		_, err := m.ReceiveTransfer(context.Background(), "web", domain.SessionTransfer{
			TransferID: "trtest00001", HeadSHA: strings.Repeat("a", 40), Branch: "feat/x", PreservedRef: ref,
		}, "")
		if !errors.Is(err, ErrTransferUnsupported) {
			t.Fatalf("preserved ref %q: err = %v, want ErrTransferUnsupported", ref, err)
		}
	}
}

func TestAssembleTransferArchiveWithoutUpload(t *testing.T) {
	m := &Manager{dataDir: t.TempDir()}
	_, err := m.assembleTransferArchive("trtest00001", domain.SessionTransferNative{Size: 10, SHA256: strings.Repeat("0", 64)})
	if !errors.Is(err, ErrTransferArchive) {
		t.Fatalf("err = %v, want ErrTransferArchive", err)
	}
}

func TestSweepTransfersDropsStaleUploadsSooner(t *testing.T) {
	now := time.Now()
	m := &Manager{dataDir: t.TempDir(), clock: func() time.Time { return now }}
	root := filepath.Join(m.dataDir, "transfers")
	upload := filepath.Join(root, "trupload0001")
	prepared := filepath.Join(root, "trsource0001")
	for _, dir := range []string{filepath.Join(upload, "incoming"), prepared} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(prepared, "payload.json"), []byte(`{"transferId":"trsource0001"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	for _, dir := range []string{upload, prepared} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	m.sweepTransfers(context.Background())
	if _, err := os.Stat(upload); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("upload kept after two hours: %v", err)
	}
	if _, err := os.Stat(prepared); err != nil {
		t.Fatalf("prepared transfer swept before seven days: %v", err)
	}
}

func TestArrivedWorkerIsAnnouncedToTheOrchestrator(t *testing.T) {
	st := newFakeStore()
	st.sessions["web-1"] = pastStartupGate(domain.SessionRecord{ID: "web-1", ProjectID: "web", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode})
	msg := &fakeMessenger{}
	m := New(Deps{Store: st, Messenger: msg})
	m.runBackground = func(work func()) { work() }
	transfer := domain.SessionTransfer{
		Source: domain.SessionTransferSource{Label: "MacBook A", SessionID: "web-3"}, Branch: "feat/x",
		DisplayName: "Fix login", IssueID: "42", PullRequests: []string{"#12"},
	}

	m.tellOrchestratorAboutArrival(context.Background(), domain.SessionRecord{ID: "web-9", ProjectID: "web"}, transfer)
	if len(msg.msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msg.msgs))
	}
	for _, want := range []string{"Worker web-9 moved here from MacBook A, where it was session web-3.", "Task: Fix login.", "Branch: feat/x.", "Issue: 42.", "Pull requests: #12.", "Do not respawn its work."} {
		if !strings.Contains(msg.msgs[0], want) {
			t.Fatalf("note missing %q:\n%s", want, msg.msgs[0])
		}
	}

	m.tellOrchestratorAboutArrival(context.Background(), domain.SessionRecord{ID: "api-9", ProjectID: "api"}, transfer)
	if len(msg.msgs) != 1 {
		t.Fatalf("a project without an orchestrator was messaged: %v", msg.msgs)
	}
}
