package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type renderDetail struct {
	Event  string `json:"event"`
	Render struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Height  int      `json:"height"`
		Path    string   `json:"path"`
		Heights [][2]int `json:"heights"`
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
	// The render route adds the bootstrap when it serves the page.
	if string(page) != "<!doctype html><p>chart</p>" {
		t.Errorf("stored page = %.200q, want the agent's raw page", page)
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
	// The page has no turn to show in, so publishing does not wait on a measure.
	h.svc.SetRenderMeasure(func(context.Context, domain.SessionID, map[string]any) (any, error) {
		t.Error("measured a page that has no turn to show in")
		return nil, nil
	})

	_, err := h.svc.PublishRender(context.Background(), testSession, chatsvc.RenderInput{
		HTML: "<p>x</p>", Title: "x", Height: 200, BaseURL: "http://127.0.0.1:3001",
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
		"empty html":  {HTML: "  ", Title: "x"},
		"no title":    {HTML: "<p>x</p>", Title: "   "},
		"over 25 MiB": {HTML: strings.Repeat("a", 25<<20+1), Title: "x"},
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

// The CLI inlines local images, so a page runs far past the HTML file an agent writes.
func TestRenderAcceptsAPageOf25MiB(t *testing.T) {
	h, _ := steerHarness(t)
	ctx := context.Background()
	h.svc.SetRenderCheck(func(context.Context, domain.SessionID, map[string]any) (any, error) {
		return map[string]any{"data": "iVBORw0KGgo=", "width": 720.0, "height": 200.0, "contentHeight": 200.0}, nil
	})
	page := "<p>" + strings.Repeat("A", 25<<20-len("<p>"))

	if _, err := h.svc.PublishRender(ctx, testSession, chatsvc.RenderInput{HTML: page, Title: "x"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := h.svc.CheckRender(ctx, testSession, chatsvc.RenderCheckInput{HTML: page, BaseURL: "http://127.0.0.1:3001"}); err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, err := h.svc.CheckRender(ctx, testSession, chatsvc.RenderCheckInput{HTML: page + "A"}); !errors.Is(err, chatsvc.ErrRenderInvalid) {
		t.Fatalf("check over 25 MiB: err = %v, want ErrRenderInvalid", err)
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
	// The desktop app loads it through the render route, which adds the bootstrap.
	if string(pageDuringCheck) != "<p>chart</p>" {
		t.Fatalf("page during the check = %.200q, want the agent's raw page", pageDuringCheck)
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

// publishMeasured publishes a page with measure installed on h and returns
// the result and the recorded render row.
func publishMeasured(t *testing.T, h *harness, measure chatsvc.RenderMeasure) (chatsvc.RenderResult, domain.ConversationActivity) {
	t.Helper()
	h.svc.SetRenderMeasure(measure)
	result, err := h.svc.PublishRender(context.Background(), testSession, chatsvc.RenderInput{
		HTML: "<p>chart</p>", Title: "Chart", Height: 400, BaseURL: "http://127.0.0.1:3001/",
	})
	if err != nil {
		t.Fatalf("PublishRender: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(renderRows(s)) == 1 })
	return result, renderRows(snapshot)[0]
}

func TestPublishRenderRecordsTheMeasuredHeightsSortedByWidth(t *testing.T) {
	h, _ := steerHarness(t)
	var gotArgs map[string]any
	var pageDuringMeasure []byte
	result, row := publishMeasured(t, h, func(ctx context.Context, id domain.SessionID, args map[string]any) (any, error) {
		gotArgs = args
		renderID := strings.TrimPrefix(args["url"].(string), "http://127.0.0.1:3001/api/v1/sessions/"+string(id)+"/renders/")
		if file, _, err := h.renders.OpenRender(ctx, id, renderID); err == nil {
			pageDuringMeasure, _ = io.ReadAll(file)
			_ = file.Close()
		}
		// Decoded JSON from the desktop app, out of order.
		return map[string]any{"heights": []any{
			[]any{1144.0, 88.0}, []any{320.0, 313.0}, []any{860.0, 120.0}, []any{375.0, 270.0}, []any{430.0, 233.0},
			[]any{520.0, 193.0}, []any{640.0, 157.0}, []any{728.0, 138.0}, []any{1000.0, 100.0},
		}}, nil
	})
	if want := "http://127.0.0.1:3001" + result.Path; gotArgs["url"] != want {
		t.Errorf("url = %v, want %q", gotArgs["url"], want)
	}
	if want := []int{320, 375, 430, 520, 640, 728, 860, 1000, 1144}; !reflect.DeepEqual(gotArgs["widths"], want) {
		t.Errorf("widths = %v, want %v", gotArgs["widths"], want)
	}
	if string(pageDuringMeasure) != "<p>chart</p>" {
		t.Errorf("page during the measure = %q, want the stored page", pageDuringMeasure)
	}
	var d renderDetail
	_ = json.Unmarshal(row.Detail, &d)
	want := [][2]int{{320, 313}, {375, 270}, {430, 233}, {520, 193}, {640, 157}, {728, 138}, {860, 120}, {1000, 100}, {1144, 88}}
	if !reflect.DeepEqual(d.Render.Heights, want) || d.Render.Height != 400 {
		t.Errorf("height = %d, heights = %v, want 400 and %v", d.Render.Height, d.Render.Heights, want)
	}
}

func TestPublishRenderWithoutMeasuredHeightsOmitsThem(t *testing.T) {
	desktopErr := errors.New("render measure timed out after 20000 ms while loading the page")
	for name, measure := range map[string]chatsvc.RenderMeasure{
		"not wired": nil,
		"no desktop app": func(context.Context, domain.SessionID, map[string]any) (any, error) {
			return nil, chatsvc.ErrRenderCheckUnavailable
		},
		"desktop error": func(context.Context, domain.SessionID, map[string]any) (any, error) {
			return nil, desktopErr
		},
		"too few heights": func(context.Context, domain.SessionID, map[string]any) (any, error) {
			return map[string]any{"heights": []any{[]any{320.0, 200.0}}}, nil
		},
		"height out of range": func(_ context.Context, _ domain.SessionID, args map[string]any) (any, error) {
			heights := []any{}
			for _, width := range args["widths"].([]int) {
				heights = append(heights, []any{float64(width), 5000.0})
			}
			return map[string]any{"heights": heights}, nil
		},
		"not heights": func(context.Context, domain.SessionID, map[string]any) (any, error) {
			return "PNG", nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := steerHarness(t)
			_, row := publishMeasured(t, h, measure)
			if strings.Contains(string(row.Detail), "heights") {
				t.Fatalf("detail = %s, want no heights", row.Detail)
			}
		})
	}
}

func TestPublishRenderStopsWaitingForAMeasureAtTheTimeout(t *testing.T) {
	restore := chatsvc.SetRenderMeasureTimeout(50 * time.Millisecond)
	t.Cleanup(restore)
	h, _ := steerHarness(t)
	var deadline time.Time
	started := time.Now()
	_, row := publishMeasured(t, h, func(ctx context.Context, _ domain.SessionID, _ map[string]any) (any, error) {
		deadline, _ = ctx.Deadline()
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("publish took %v", elapsed)
	}
	if deadline.IsZero() || deadline.Sub(started) > time.Second {
		t.Fatalf("measure deadline %v after the start, want the 50ms timeout", deadline.Sub(started))
	}
	if strings.Contains(string(row.Detail), "heights") {
		t.Fatalf("detail = %s, want no heights", row.Detail)
	}
}
