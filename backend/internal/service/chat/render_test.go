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

func TestCheckRenderLoadsTheStoredPageAndDeletesIt(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessCodex)
	var gotArgs map[string]any
	var pageDuringCheck []byte
	h.svc.SetRenderCheck(func(ctx context.Context, id domain.SessionID, args map[string]any) (any, error) {
		gotArgs = args
		renderID := strings.TrimPrefix(args["url"].(string), "http://127.0.0.1:3001/api/v1/sessions/"+string(id)+"/renders/")
		file, _, err := h.renders.OpenRender(ctx, id, renderID)
		if err != nil {
			t.Fatalf("page not stored during the check: %v", err)
		}
		pageDuringCheck, _ = io.ReadAll(file)
		_ = file.Close()
		return map[string]any{
			"data": "iVBORw0KGgo=", "width": 720.0, "height": 412.0, "contentHeight": 412.0,
			"consoleMessages": []any{map[string]any{"level": "error", "text": "Uncaught ReferenceError: d3 is not defined"}},
		}, nil
	})

	result, err := h.svc.CheckRender(context.Background(), testSession, chatsvc.RenderCheckInput{
		HTML: "<p>chart</p>", BaseURL: "http://127.0.0.1:3001",
	})
	if err != nil {
		t.Fatalf("CheckRender: %v", err)
	}
	if !strings.HasPrefix(gotArgs["url"].(string), "http://127.0.0.1:3001/api/v1/sessions/"+string(testSession)+"/renders/check-") || gotArgs["width"] != 720 {
		t.Fatalf("args = %v", gotArgs)
	}
	if !strings.Contains(string(pageDuringCheck), `<style id="ao-theme">`) {
		t.Fatal("the check did not see the bootstrapped page readers get")
	}
	if result.ContentHeight != 412 || result.PNG != "iVBORw0KGgo=" || len(result.ConsoleMessages) != 1 || result.ConsoleMessages[0].Level != "error" {
		t.Fatalf("result = %+v", result)
	}
	entries, _ := os.ReadDir(filepath.Join(h.rendersDir, "attachments", string(testSession)))
	if len(entries) != 0 {
		t.Fatalf("check left files behind: %v", entries)
	}
}

func TestCheckRenderWithoutTheDesktopAppSaysSo(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessCodex)
	h.svc.SetRenderCheck(func(context.Context, domain.SessionID, map[string]any) (any, error) {
		return nil, chatsvc.ErrRenderCheckUnavailable
	})
	_, err := h.svc.CheckRender(context.Background(), testSession, chatsvc.RenderCheckInput{HTML: "<p>x</p>", BaseURL: "http://127.0.0.1:3001"})
	if !errors.Is(err, chatsvc.ErrRenderCheckUnavailable) {
		t.Fatalf("err = %v, want ErrRenderCheckUnavailable", err)
	}
	if _, err := h.svc.CheckRender(context.Background(), testSession, chatsvc.RenderCheckInput{HTML: "<p>x</p>", Width: 100}); !errors.Is(err, chatsvc.ErrRenderInvalid) {
		t.Fatalf("width 100: err = %v, want ErrRenderInvalid", err)
	}
}
