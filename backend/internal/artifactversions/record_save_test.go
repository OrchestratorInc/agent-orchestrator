package artifactversions

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRecordSaveStampsTheUserAndReadVersionReturnsTheContent(t *testing.T) {
	h := newHarness(t)
	h.write("plan.md", "agent draft")
	h.tick()
	h.tick()
	if vs := h.versions("plan.md"); len(vs) != 1 || vs[0].Source != domain.ArtifactVersionSourceAgent {
		t.Fatalf("after the agent's write: %+v, want one agent version", vs)
	}

	h.write("plan.md", "user edit")
	if err := h.cap.RecordSave(h.ctx, h.sess, "plan.md"); err != nil {
		t.Fatalf("RecordSave: %v", err)
	}
	vs := h.versions("plan.md")
	if len(vs) == 2 && (vs[0].Harness != domain.HarnessClaudeCode || vs[1].Harness != domain.HarnessClaudeCode) {
		t.Fatalf("harness = %q / %q, want the session's agent on every version", vs[0].Harness, vs[1].Harness)
	}
	if len(vs) != 2 || vs[0].Version != 2 || vs[0].Source != domain.ArtifactVersionSourceUser {
		t.Fatalf("after the save: %+v, want version 2 written by the user", vs)
	}
	// The observer's next ticks see the saved file already versioned.
	h.tick()
	h.tick()
	if got := len(h.versions("plan.md")); got != 2 {
		t.Fatalf("versions after ticks = %d, want 2", got)
	}

	data, err := h.cap.ReadVersion(h.sess, vs[1])
	if err != nil || string(data) != "agent draft" {
		t.Fatalf("ReadVersion(1) = %q, %v", data, err)
	}
}

func TestRecordRestoreNamesTheVersionItRestored(t *testing.T) {
	h := newHarness(t)
	h.write("plan.md", "first")
	h.tick()
	h.tick()
	h.write("plan.md", "second")
	h.tick()
	h.tick()
	h.write("plan.md", "first")
	if err := h.cap.RecordRestore(h.ctx, h.sess, "plan.md", 1); err != nil {
		t.Fatalf("RecordRestore: %v", err)
	}
	vs := h.versions("plan.md")
	if len(vs) != 3 || vs[0].Source != domain.ArtifactVersionSourceUser || vs[0].RestoredFrom != 1 || vs[1].RestoredFrom != 0 {
		t.Fatalf("versions = %+v, want a third, user-written one that restored version 1", vs)
	}
}

func TestRecordSaveOfUnchangedContentRecordsNothing(t *testing.T) {
	h := newHarness(t)
	h.write("plan.md", "same")
	h.tick()
	h.tick()
	h.touch("plan.md")
	if err := h.cap.RecordSave(h.ctx, h.sess, "plan.md"); err != nil {
		t.Fatalf("RecordSave: %v", err)
	}
	if got := len(h.versions("plan.md")); got != 1 {
		t.Fatalf("versions = %d, want 1", got)
	}
}

func TestReadVersionRefusesAMalformedHash(t *testing.T) {
	h := newHarness(t)
	_, err := h.cap.ReadVersion(h.sess, domain.SessionArtifactVersion{SHA256: "../../etc/passwd"})
	if err == nil {
		t.Fatal("ReadVersion accepted a path-shaped hash")
	}
}
