package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type renderDetail struct {
	Event  string `json:"event"`
	Render struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Height int    `json:"height"`
		Path   string `json:"path"`
	} `json:"render"`
}

func renderRows(s store.ConversationSnapshot) []domain.ConversationActivity {
	var rows []domain.ConversationActivity
	for _, a := range s.Activities {
		var d renderDetail
		if a.Kind == domain.ActivityKindSystem && json.Unmarshal(a.Detail, &d) == nil && d.Event == "render" {
			rows = append(rows, a)
		}
	}
	return rows
}

func TestPublishRenderLandsOnTheRunningTurn(t *testing.T) {
	h, _ := steerHarness(t)
	ctx := context.Background()

	result, err := h.svc.PublishRender(ctx, testSession, chatsvc.RenderInput{
		HTML: "<!doctype html><p>chart</p>", Title: "  Turns by day  ", Height: 5000,
	})
	if err != nil {
		t.Fatalf("PublishRender: %v", err)
	}
	wantPath := "/api/v1/sessions/" + string(testSession) + "/renders/" + result.RenderID
	if result.Path != wantPath {
		t.Errorf("path = %q, want %q", result.Path, wantPath)
	}

	file, _, err := h.renders.OpenRender(ctx, testSession, result.RenderID)
	if err != nil {
		t.Fatalf("stored page: %v", err)
	}
	page, _ := io.ReadAll(file)
	_ = file.Close()
	if !strings.Contains(string(page), `<style id="ao-theme">`) || !strings.HasSuffix(string(page), "<p>chart</p>") {
		t.Errorf("stored page lacks the bootstrap or the agent's body: %.200s", page)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(renderRows(s)) == 1 })
	row := renderRows(snapshot)[0]
	var d renderDetail
	_ = json.Unmarshal(row.Detail, &d)
	if d.Render.ID != result.RenderID || d.Render.Title != "Turns by day" || d.Render.Height != 2000 || d.Render.Path != wantPath {
		t.Errorf("detail = %+v", d)
	}
	if row.Summary != "Turns by day" || row.Status != domain.ActivityStatusCompleted {
		t.Errorf("row summary=%q status=%q", row.Summary, row.Status)
	}
	var running string
	for _, turn := range snapshot.Turns {
		if turn.ProviderTurnID == "provider-turn-1" {
			running = turn.ID
		}
	}
	if running == "" || row.TurnID != running {
		t.Errorf("render on turn %q, want the running turn %q", row.TurnID, running)
	}
}

func TestPublishRenderWithoutARunningTurnLeavesNoFile(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessCodex)

	_, err := h.svc.PublishRender(context.Background(), testSession, chatsvc.RenderInput{
		HTML: "<p>x</p>", Title: "x", Height: 200,
	})
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("err = %v, want ErrNoActiveTurn", err)
	}
	entries, _ := os.ReadDir(filepath.Join(h.rendersDir, "attachments", string(testSession)))
	if len(entries) != 0 {
		t.Fatalf("orphan render files: %v", entries)
	}
}

func TestPublishRenderRejectsBadPagesBeforeStoring(t *testing.T) {
	h, _ := steerHarness(t)
	for name, in := range map[string]chatsvc.RenderInput{
		"empty html": {HTML: "  ", Title: "x"},
		"no title":   {HTML: "<p>x</p>", Title: "   "},
		"over 1 MiB": {HTML: strings.Repeat("a", 1<<20+1), Title: "x"},
	} {
		if _, err := h.svc.PublishRender(context.Background(), testSession, in); !errors.Is(err, chatsvc.ErrRenderInvalid) {
			t.Errorf("%s: err = %v, want ErrRenderInvalid", name, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(h.rendersDir, "attachments", string(testSession)))
	if len(entries) != 0 {
		t.Fatalf("invalid pages were stored: %v", entries)
	}
}
