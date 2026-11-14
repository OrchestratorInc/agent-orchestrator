//go:build unix

package chat_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

func TestRecordReportedArtifactSkipsAFIFOWithoutBlocking(t *testing.T) {
	h, _ := steerHarness(t)
	dir := sessionartifacts.Dir(h.rendersDir, testSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.html"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.svc.RecordReportedArtifact(context.Background(), testSession, "pipe.html")
	if rows := artifactRows(t, h); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}

func TestRecordReportedArtifactSkipsABackslashName(t *testing.T) {
	h, _ := steerHarness(t)
	writeArtifact(t, sessionartifacts.Dir(h.rendersDir, testSession), `q3\report.html`)
	h.svc.RecordReportedArtifact(context.Background(), testSession, `q3\report.html`)
	if rows := artifactRows(t, h); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}
