package controllers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

type streamingCloneManager struct {
	projectsvc.Manager
	err error
}

func (m streamingCloneManager) PrepareCloneWithProgress(_ context.Context, in projectsvc.CloneInput, onProgress func(projectsvc.CloneProgress)) (projectsvc.ClonePreparationResult, error) {
	percent := 45
	onProgress(projectsvc.CloneProgress{Phase: projectsvc.ClonePhaseReceivingObjects, Attempt: 1, Percent: &percent})
	onProgress(projectsvc.CloneProgress{Phase: projectsvc.ClonePhaseResolvingDeltas, Attempt: 1})
	if m.err != nil {
		return projectsvc.ClonePreparationResult{}, m.err
	}
	return projectsvc.ClonePreparationResult{Path: "/code/" + in.RemoteURL, RemoteURL: in.RemoteURL, PreparationID: "prep"}, nil
}

func streamCloneEvents(t *testing.T, mgr projectsvc.Manager) []controllers.ClonePrepareStreamEvent {
	t.Helper()
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{
		Projects: mgr,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/api/v1/projects/clone/prepare/stream", "application/json", strings.NewReader(`{"remoteUrl":"repo","destinationParent":"/code"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status = %d, content type = %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var events []controllers.ClonePrepareStreamEvent
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var event controllers.ClonePrepareStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("event %q: %v", data, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestPrepareCloneStreamSendsProgressThenResult(t *testing.T) {
	events := streamCloneEvents(t, streamingCloneManager{})
	if len(events) != 3 {
		t.Fatalf("events = %+v, want two progress events and a result", events)
	}
	if events[0].Progress == nil || events[0].Progress.Phase != projectsvc.ClonePhaseReceivingObjects ||
		events[0].Progress.Percent == nil || *events[0].Progress.Percent != 45 ||
		events[1].Progress == nil || events[1].Progress.Phase != projectsvc.ClonePhaseResolvingDeltas || events[1].Progress.Percent != nil {
		t.Fatalf("progress events = %+v", events[:2])
	}
	if events[2].Result == nil || events[2].Result.Path != "/code/repo" || events[2].Error != nil {
		t.Fatalf("final event = %+v, want the result", events[2])
	}
}

func TestPrepareCloneStreamEndsWithErrorEnvelope(t *testing.T) {
	events := streamCloneEvents(t, streamingCloneManager{err: apierr.Invalid("GIT_CLONE_FAILED", "Could not clone this repository.", nil)})
	if len(events) != 3 {
		t.Fatalf("events = %+v, want two progress events and an error", events)
	}
	final := events[2]
	if final.Error == nil || final.Result != nil {
		t.Fatalf("final event = %+v, want the error envelope", final)
	}
	if final.Error.Code != "GIT_CLONE_FAILED" || final.Error.Error != "bad_request" || final.Error.Message != "Could not clone this repository." {
		t.Fatalf("error envelope = %+v", final.Error)
	}
}
