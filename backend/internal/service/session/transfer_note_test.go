package session

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTransferResumeNoteNamesTheOrchestrator(t *testing.T) {
	transfer := domain.SessionTransfer{Source: domain.SessionTransferSource{Label: "MacBook A"}, Branch: "feat/x"}
	if note := transferResumeNote(transfer, "web-1"); !strings.Contains(note, "You now report to the orchestrator web-1 on this computer; the one on MacBook A cannot be reached from here.") {
		t.Fatalf("note with an orchestrator:\n%s", note)
	}
	if note := transferResumeNote(transfer, ""); !strings.Contains(note, "This project has no orchestrator on this computer, and the one on MacBook A cannot be reached from here.") {
		t.Fatalf("note without an orchestrator:\n%s", note)
	}
}
