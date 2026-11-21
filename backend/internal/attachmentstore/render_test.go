package attachmentstore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRenderRoundTripStaysOutOfWorkspaceAndPreview(t *testing.T) {
	ctx := context.Background()
	store := New(t.TempDir())
	session := domain.SessionID("proj-1")

	if err := store.PutRender(ctx, session, "r1", []byte("<p>hi</p>")); err != nil {
		t.Fatalf("PutRender: %v", err)
	}
	file, _, err := store.OpenRender(ctx, session, "r1")
	if err != nil {
		t.Fatalf("OpenRender: %v", err)
	}
	body, _ := io.ReadAll(file)
	_ = file.Close()
	if string(body) != "<p>hi</p>" {
		t.Fatalf("body = %q", body)
	}

	// The workspace preview origin may call the daemon API, so it must never serve a render.
	if _, ok := NameFromWorkspacePath(".ao/attachments/" + renderName("r1")); ok {
		t.Fatal("preview path resolved a render file")
	}
	if _, _, err := store.Open(ctx, session, renderName("r1")); err == nil {
		t.Fatal("attachment Open served a render file")
	}
	workspace := t.TempDir()
	if _, err := store.MaterializeWorkspace(ctx, session, workspace, nil); err != nil {
		t.Fatalf("MaterializeWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, WorkspaceDir, renderName("r1"))); !os.IsNotExist(err) {
		t.Fatalf("render projected into the worktree: %v", err)
	}

	if err := store.RemoveRender(ctx, session, "r1"); err != nil {
		t.Fatalf("RemoveRender: %v", err)
	}
	if _, _, err := store.OpenRender(ctx, session, "r1"); err == nil {
		t.Fatal("render still readable after RemoveRender")
	}
}

func TestRenderIDsRejectTraversalAndDots(t *testing.T) {
	store := New(t.TempDir())
	for _, id := range []string{"", "../x", "a/b", `a\b`, "a.b", ".."} {
		if err := store.PutRender(context.Background(), "proj-1", id, []byte("x")); err == nil {
			t.Errorf("PutRender(%q) accepted", id)
		}
		if _, _, err := store.OpenRender(context.Background(), "proj-1", id); err == nil {
			t.Errorf("OpenRender(%q) opened something", id)
		}
	}
}
