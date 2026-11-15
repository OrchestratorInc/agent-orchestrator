//go:build unix

package chat_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// A FIFO under the render's name must not hang the same-bytes check.
func TestSaveRenderAsArtifactSkipsAFIFOWithoutBlocking(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessCodex)
	ctx := context.Background()
	if err := h.renders.PutRender(ctx, testSession, "r1", []byte("<p>x</p>")); err != nil {
		t.Fatal(err)
	}
	dir := sessionartifacts.Dir(h.rendersDir, testSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "Chart.html"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := h.svc.SaveRenderAsArtifact(ctx, testSession, "r1", "Chart")
	if err != nil || got.Path != "Chart (2).html" {
		t.Fatalf("artifact = %+v, %v; want Chart (2).html", got, err)
	}
}
