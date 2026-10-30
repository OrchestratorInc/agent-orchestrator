package chat_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type artifactDetail struct {
	Event    string `json:"event"`
	Artifact struct {
		Path string `json:"path"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"artifact"`
}

// artifactRows reads the conversation's "artifact" activities straight from the
// store: a report records them before it returns.
func artifactRows(t *testing.T, h *harness) []artifactDetail {
	t.Helper()
	snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	var rows []artifactDetail
	for _, a := range snapshot.Activities {
		var d artifactDetail
		if a.Kind == domain.ActivityKindSystem && json.Unmarshal(a.Detail, &d) == nil && d.Event == "artifact" {
			if a.Summary != d.Artifact.Name || a.Status != domain.ActivityStatusCompleted {
				t.Errorf("artifact row summary=%q status=%q", a.Summary, a.Status)
			}
			rows = append(rows, d)
		}
	}
	return rows
}

func writeArtifact(t *testing.T, dir, rel string) string {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("<p>report</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestRecordReportedArtifactShowsTheHTMLPageInTheRunningTurn(t *testing.T) {
	h, _ := steerHarness(t)
	ctx := context.Background()
	// The session's stored artifact directory wins over the default one.
	dir := t.TempDir()
	if _, err := h.st.UpdateSessionArtifactOutput(ctx, testSession, dir, domain.SessionOutputNone); err != nil {
		t.Fatal(err)
	}
	page := writeArtifact(t, dir, "q3/Q3 (final).html")
	writeArtifact(t, dir, "summary.HTM")

	h.svc.RecordReportedArtifact(ctx, testSession, page)
	rows := artifactRows(t, h)
	want := artifactDetail{Event: "artifact"}
	want.Artifact.Path = "q3/Q3 (final).html"
	want.Artifact.Name = "Q3 (final).html"
	want.Artifact.URL = "/api/v1/sessions/" + string(testSession) + "/artifact-files/q3/Q3%20%28final%29.html"
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}

	// The same file again in this turn, by a relative path, keeps its one row.
	h.svc.RecordReportedArtifact(ctx, testSession, "./q3/../q3/Q3 (final).html")
	if rows := artifactRows(t, h); len(rows) != 1 {
		t.Fatalf("rows after a repeat = %+v, want one", rows)
	}

	h.svc.RecordReportedArtifact(ctx, testSession, "summary.HTM")
	rows = artifactRows(t, h)
	if len(rows) != 2 || rows[1].Artifact.Path != "summary.HTM" || rows[1].Artifact.Name != "summary.HTM" {
		t.Fatalf("rows = %+v, want summary.HTM second", rows)
	}
}

func TestRecordReportedArtifactSkipsWhatItCannotShow(t *testing.T) {
	h, _ := steerHarness(t)
	ctx := context.Background()
	dir := sessionartifacts.Dir(h.rendersDir, testSession)
	writeArtifact(t, dir, "notes.md")
	writeArtifact(t, dir, "folder.html/index.html")
	// One byte past what the artifact file route serves; sparse, so cheap.
	huge := writeArtifact(t, dir, "huge.html")
	if err := os.Truncate(huge, attachmentstore.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	outside := writeArtifact(t, t.TempDir(), "outside.html")
	if err := os.Symlink(outside, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{
		"notes.md",
		filepath.Join(dir, "notes.md"),
		outside,
		"../outside.html",
		"escape.html",
		"folder.html",
		"huge.html",
		"gone.html",
		"",
	} {
		h.svc.RecordReportedArtifact(ctx, testSession, reference)
	}
	if rows := artifactRows(t, h); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}

	// A terminal session has no thread.
	tui := domain.SessionID("p1-2")
	tuiDir := t.TempDir()
	page := writeArtifact(t, tuiDir, "page.html")
	if _, err := h.st.CreateSession(ctx, domain.SessionRecord{
		ID: tui, ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{ArtifactDir: tuiDir}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.RecordReportedArtifact(ctx, tui, page)
	if rows := artifactRows(t, h); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}

	// A page in the default directory (no stored one) does show.
	writeArtifact(t, dir, "page.html")
	h.svc.RecordReportedArtifact(ctx, testSession, "page.html")
	if rows := artifactRows(t, h); len(rows) != 1 || rows[0].Artifact.Path != "page.html" {
		t.Fatalf("rows = %+v, want page.html alone", rows)
	}
}

func TestRecordReportedArtifactWithoutARunningTurnShowsNothing(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()
	page := writeArtifact(t, sessionartifacts.Dir(h.rendersDir, testSession), "page.html")
	h.svc.RecordReportedArtifact(ctx, testSession, page)
	if rows := artifactRows(t, h); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}

	// The same controller shows the page once a turn runs, so the skip above
	// was the missing turn, not a missing controller.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.svc.RecordReportedArtifact(ctx, testSession, page)
	if rows := artifactRows(t, h); len(rows) != 1 {
		t.Fatalf("rows = %+v, want one once a turn runs", rows)
	}
}

// An agent that keeps a render as an artifact and then reports that file would
// otherwise show the same page twice in the turn.
func TestRecordReportedArtifactSkipsARenderKeptInTheSameTurn(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	result, err := h.svc.PublishRender(ctx, testSession, chatsvc.RenderInput{HTML: "<p>q3</p>", Title: "Q3 status", Artifact: true})
	if err != nil || result.ArtifactPath == "" {
		t.Fatalf("PublishRender = %+v, %v", result, err)
	}
	h.svc.RecordReportedArtifact(ctx, testSession, result.ArtifactPath)
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if renders, artifacts := renderRows(snapshot), artifactRows(t, h); len(renders) != 1 || len(artifacts) != 0 {
		t.Fatalf("renders = %d, artifacts = %+v; want the render alone", len(renders), artifacts)
	}

	// A different file in the same turn still shows.
	writeArtifact(t, filepath.Dir(result.ArtifactPath), "appendix.html")
	h.svc.RecordReportedArtifact(ctx, testSession, "appendix.html")
	if rows := artifactRows(t, h); len(rows) != 1 || rows[0].Artifact.Path != "appendix.html" {
		t.Fatalf("rows = %+v, want appendix.html", rows)
	}

	// The kept page reported in the next turn shows there.
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "again", ClientMessageID: "turn-2", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(provider.sentTexts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-2"})
	h.svc.RecordReportedArtifact(ctx, testSession, result.ArtifactPath)
	if rows := artifactRows(t, h); len(rows) != 2 || rows[1].Artifact.Path != "Q3 status.html" {
		t.Fatalf("rows = %+v, want Q3 status.html in the next turn", rows)
	}
}
