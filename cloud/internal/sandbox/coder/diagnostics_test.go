package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

func TestDiagnoseReadsBuildAndAgentLogTails(t *testing.T) {
	t.Parallel()
	const buildID = "8d3b6f6e-4d2b-4a63-9a8e-3b2b2d6a7c01"
	var requests []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests = append(requests, request.URL.Path)
		mu.Unlock()
		if request.Header.Get("Coder-Session-Token") != "test-token" {
			t.Errorf("missing session token on %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v2/workspaces/" + testWorkspaceID:
			view := healthyWorkspace()
			view.LatestBuild.ID = buildID
			view.LatestBuild.Status = "failed"
			view.LatestBuild.Job = workspaceBuildJob{Status: "failed", Error: "quota exceeded", ErrorCode: "REQUIRED_TEMPLATE_VARIABLES"}
			view.LatestBuild.Resources[0].Agents[0].LifecycleState = "start_error"
			_ = json.NewEncoder(writer).Encode(view)
		case "/api/v2/workspacebuilds/" + buildID + "/logs":
			lines := make([]buildLogLine, 0, 60)
			for index := range 60 {
				lines = append(lines, buildLogLine{Stage: "Setting up", Output: fmt.Sprintf("build line %d", index)})
			}
			_ = json.NewEncoder(writer).Encode(lines)
		case "/api/v2/workspaceagents/" + testAgentID + "/logs":
			_ = json.NewEncoder(writer).Encode([]agentLogLine{{Output: "git clone failed\r\n"}, {Output: "exit status 128"}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, nil)
	diagnostics, err := client.Diagnose(context.Background(), sandbox.ID(testWorkspaceID))
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	summary := diagnostics.Summary
	if summary["job_error"] != "quota exceeded" || summary["agent_lifecycle"] != "start_error" ||
		summary["build_status"] != "failed" || summary["coder_owner"] != "ao-integration" ||
		summary["coder_template"] != testTemplateID || summary["workspace_id"] != testWorkspaceID {
		t.Fatalf("summary = %v", summary)
	}
	for key, value := range summary {
		if strings.Contains(value, "test-token") {
			t.Fatalf("summary %s leaked the token", key)
		}
	}
	if len(diagnostics.BuildLog) != diagnosticLogLines || diagnostics.BuildLog[len(diagnostics.BuildLog)-1] != "[Setting up] build line 59" {
		t.Fatalf("build log tail = %v", diagnostics.BuildLog)
	}
	if len(diagnostics.AgentLog) != 2 || diagnostics.AgentLog[0] != "git clone failed" {
		t.Fatalf("agent log = %q", diagnostics.AgentLog)
	}
}

func TestDiagnoseReturnsWhatItGathered(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID {
			view := healthyWorkspace()
			view.LatestBuild.ID = "8d3b6f6e-4d2b-4a63-9a8e-3b2b2d6a7c01"
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(view)
			return
		}
		http.Error(writer, `{"message":"forbidden"}`, http.StatusForbidden)
	}))
	defer server.Close()
	diagnostics, err := newTestClient(t, server.URL, nil).Diagnose(context.Background(), sandbox.ID(testWorkspaceID))
	if err == nil || !strings.Contains(err.Error(), "build log") {
		t.Fatalf("err = %v, want the build log read error", err)
	}
	if diagnostics.Summary["agent_status"] != "connected" {
		t.Fatalf("summary = %v, want what was gathered before the error", diagnostics.Summary)
	}
}

func TestBootstrapReportsAgentWaitStage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		view := healthyWorkspace()
		view.Name = WorkspaceName("session-1")
		view.LatestBuild.Resources[0].Agents[0].LifecycleState = "starting"
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(view)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)

	var reports []sandbox.StageReport
	ctx := sandbox.WithStageObserver(context.Background(), func(_ context.Context, report sandbox.StageReport) {
		reports = append(reports, report)
	})
	err := client.BootstrapWorker(ctx, sandbox.ID(testWorkspaceID), sandbox.WorkerBootstrap{
		Binary: []byte("worker"), Destination: "/usr/local/bin/ao-worker",
		HelperBinary: []byte("helper"), HelperDestination: "/usr/local/bin/ao",
		User: "ao-worker", DurableRoot: "/persistent/coder", DurableIdentity: "session-1",
		Environment: map[string]string{"AO_CONTROL_PLANE_URL": "https://cp.example.com"},
	})
	if !errors.Is(err, sandbox.ErrWorkspaceNotReady) {
		t.Fatalf("err = %v, want not ready", err)
	}
	if len(reports) != 1 || reports[0].Stage != sandbox.StageAgentWait || !errors.Is(reports[0].Err, sandbox.ErrWorkspaceNotReady) {
		t.Fatalf("reports = %+v, want one failed agent_wait", reports)
	}
	if reports[0].Attrs["agent_lifecycle"] != "starting" || reports[0].Attrs["workspace_id"] != testWorkspaceID ||
		reports[0].Attrs["coder_owner"] != "ao-integration" {
		t.Fatalf("attrs = %v", reports[0].Attrs)
	}
}

func TestCreateAndStartReportStagesWithoutAnObserver(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, `{"message":"template version is broken"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	// No observer installed: stage reporting is a no-op.
	if err := client.Start(context.Background(), sandbox.ID(testWorkspaceID)); err == nil {
		t.Fatal("start against a failing Coder must fail")
	}
	var reports []sandbox.StageReport
	ctx := sandbox.WithStageObserver(context.Background(), func(_ context.Context, report sandbox.StageReport) {
		reports = append(reports, report)
	})
	if _, err := client.Create(ctx, sandbox.Spec{SessionID: "session-1"}); err == nil {
		t.Fatal("create against a failing Coder must fail")
	}
	if err := client.Start(ctx, sandbox.ID(testWorkspaceID)); err == nil {
		t.Fatal("start against a failing Coder must fail")
	}
	if len(reports) != 2 || reports[0].Stage != sandbox.StageWorkspaceCreate || reports[1].Stage != sandbox.StageWorkspaceStart ||
		reports[0].Err == nil || !strings.Contains(reports[1].Err.Error(), "template version is broken") {
		t.Fatalf("reports = %+v", reports)
	}
}
