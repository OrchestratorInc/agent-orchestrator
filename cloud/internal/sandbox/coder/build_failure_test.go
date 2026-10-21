package coder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

const testBuildID = "f79de6f6-2a8d-4850-9fe1-3409315eee5b"

// The build log of a real failed build (staging, 2026-10-18): the job error
// says only "terraform apply: exit status 1"; the log names the AWS quota.
var vcpuLimitBuildLog = []buildLogLine{
	{Stage: "Starting workspace", Level: "info", Output: "aws_instance.workspace: Creating..."},
	{Stage: "Starting workspace", Level: "info", Output: "aws_instance.workspace: Creation errored after 1s"},
	{Stage: "Starting workspace", Level: "error", Output: "Error: creating EC2 Instance: operation error EC2: RunInstances, https response error StatusCode: 400, RequestID: a74246f1-8db5-4c51-baed-4fed522bcbe8, api error VcpuLimitExceeded: You have requested more vCPU capacity than your current vCPU limit of 32 allows for the instance bucket that the specified instance type belongs to. Please visit http://aws.amazon.com/contact-us/ec2-request to request an adjustment to this limit."},
	{Stage: "Starting workspace", Level: "error", Output: `on main.tf line 372, in resource "aws_instance" "workspace":`},
	{Stage: "Starting workspace", Level: "error", Output: `  372: resource "aws_instance" "workspace" {`},
	{Stage: "Cleaning Up", Level: "info", Output: ""},
}

// failedBuildServer serves one workspace whose latest build ended with status
// and transition, and the build log (or a 500 when logs is nil).
func failedBuildServer(t *testing.T, status, transition string, logs []buildLogLine) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var logReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID:
			view := healthyWorkspace()
			view.LatestBuild = workspaceBuild{
				ID: testBuildID, Status: status, Transition: transition,
				Job: workspaceBuildJob{
					Status: status, Error: "terraform apply: exit status 1",
				},
			}
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(view); err != nil {
				t.Errorf("write workspace: %v", err)
			}
		case request.Method == http.MethodGet && request.URL.Path == "/api/v2/workspacebuilds/"+testBuildID+"/logs":
			logReads.Add(1)
			if logs == nil {
				http.Error(writer, "logs unavailable", http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(logs); err != nil {
				t.Errorf("write build log: %v", err)
			}
		default:
			http.Error(writer, "unexpected route", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, &logReads
}

// Get reports a failed start build with a generic reason and never reads
// the build log: it runs on every reconcile pass.
func TestGetReportsFailedStartBuildWithoutReadingItsLog(t *testing.T) {
	t.Parallel()
	server, logReads := failedBuildServer(t, "failed", "start", vcpuLimitBuildLog)
	client := newTestClient(t, server.URL, nil)
	for range 3 {
		environment, err := client.Get(context.Background(), testWorkspaceID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		failure := environment.Failure
		if environment.State != sandbox.StateFailed || failure == nil || failure.ID != testBuildID || failure.Canceled ||
			failure.Err.Code != sandbox.StartupErrorWorkspaceBuildFailed || failure.Err.Message != buildFailedGeneric {
			t.Fatalf("environment = %+v failure = %+v", environment, failure)
		}
	}
	if logReads.Load() != 0 {
		t.Fatalf("Get read the build log %d times, want 0", logReads.Load())
	}
}

func describe(t *testing.T, logs []buildLogLine, customerOwned bool) (*sandbox.StartupError, int32) {
	t.Helper()
	server, logReads := failedBuildServer(t, "failed", "start", logs)
	client := newTestClient(t, server.URL, nil)
	environment, err := client.Get(context.Background(), testWorkspaceID)
	if err != nil || environment.Failure == nil {
		t.Fatalf("get: %+v %v", environment, err)
	}
	return client.DescribeFailure(context.Background(), testWorkspaceID, *environment.Failure, customerOwned), logReads.Load()
}

// A customer's own workspace shows the build log's cause line, without the
// request id or the Terraform location lines.
func TestDescribeFailureShowsCustomerTheBuildLogCause(t *testing.T) {
	t.Parallel()
	described, logReads := describe(t, vcpuLimitBuildLog, true)
	const want = "The workspace failed to build: VcpuLimitExceeded: You have requested more vCPU capacity than your current vCPU limit of 32 allows"
	if described == nil || described.Code != sandbox.StartupErrorWorkspaceBuildFailed || !strings.HasPrefix(described.Message, want) {
		t.Fatalf("described = %+v, want prefix %q", described, want)
	}
	if strings.Contains(described.Message, "RequestID") || strings.Contains(described.Message, "main.tf") ||
		len(described.Message) > len("The workspace failed to build: ")+buildFailureMessageLimit {
		t.Fatalf("message is not a short, single cause: %q", described.Message)
	}
	if logReads != 1 {
		t.Fatalf("build log reads = %d, want 1", logReads)
	}
}

// AO's own workspace shows only the cloud API's error code: never AO's
// accounts, limits, roles or hosts. The cause line stays in Err for operators.
func TestDescribeFailureHidesManagedInfrastructure(t *testing.T) {
	t.Parallel()
	described, _ := describe(t, vcpuLimitBuildLog, false)
	const want = "The workspace failed to build: VcpuLimitExceeded (cloud capacity limit). Try again shortly."
	if described == nil || described.Message != want {
		t.Fatalf("described = %+v, want %q", described, want)
	}
	if described.Err == nil || !strings.Contains(described.Err.Error(), "vCPU limit of 32") {
		t.Fatalf("operator error = %v, want the cause line", described.Err)
	}

	internal := []buildLogLine{{Level: "error", Output: "Error: creating role: AccessDenied for arn:aws:iam::123456789012:role/ao-coder on ao-coder-internal.example.net"}}
	described, _ = describe(t, internal, false)
	if described == nil || strings.Contains(described.Message, "arn:") || strings.Contains(described.Message, "123456789012") ||
		strings.Contains(described.Message, "example.net") {
		t.Fatalf("managed message leaked infrastructure: %+v", described)
	}

	azure := []buildLogLine{{Level: "error", Output: `Error: creating Linux Virtual Machine "vm" (Subscription: "0000-1111"; Resource Group: "ao-rg"): Code="SkuNotAvailable" Message="The requested size is not available"`}}
	described, _ = describe(t, azure, false)
	if described == nil || described.Message != "The workspace failed to build: SkuNotAvailable (cloud capacity limit). Try again shortly." {
		t.Fatalf("azure described = %+v", described)
	}
}

// When the build log cannot be read, the job error explains a customer's
// build, and AO's own build gets the generic reason.
func TestDescribeFailureFallsBackToJobError(t *testing.T) {
	t.Parallel()
	described, logReads := describe(t, nil, true)
	if described == nil || described.Message != "The workspace failed to build: terraform apply: exit status 1" || logReads != 1 {
		t.Fatalf("described = %+v, log reads = %d", described, logReads)
	}
	described, _ = describe(t, nil, false)
	if described == nil || described.Message != buildFailedGeneric+tryAgainShortly {
		t.Fatalf("managed described = %+v", described)
	}
}

func TestCanceledStartBuildIsReportedWithoutItsLog(t *testing.T) {
	t.Parallel()
	server, logReads := failedBuildServer(t, "canceled", "start", vcpuLimitBuildLog)
	client := newTestClient(t, server.URL, nil)
	environment, err := client.Get(context.Background(), testWorkspaceID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if environment.State != sandbox.StateFailed || environment.Failure == nil || !environment.Failure.Canceled ||
		environment.Failure.ID != testBuildID {
		t.Fatalf("environment = %+v failure = %+v", environment, environment.Failure)
	}
	described := client.DescribeFailure(context.Background(), testWorkspaceID, *environment.Failure, true)
	if described == nil || described.Message != buildCanceledMessage || logReads.Load() != 0 {
		t.Fatalf("described = %+v, log reads = %d", described, logReads.Load())
	}
}

// A failed stop or delete is not a startup failure: the reconciler simply
// asks for the transition it wants again.
func TestFailedStopBuildCarriesNoStartupFailure(t *testing.T) {
	t.Parallel()
	for _, transition := range []string{"stop", "delete"} {
		server, logReads := failedBuildServer(t, "failed", transition, vcpuLimitBuildLog)
		environment, err := newTestClient(t, server.URL, nil).Get(context.Background(), testWorkspaceID)
		if err != nil {
			t.Fatalf("%s: get: %v", transition, err)
		}
		if environment.State != sandbox.StateFailed || environment.Failure != nil || logReads.Load() != 0 {
			t.Fatalf("%s: environment = %+v failure = %+v log reads = %d",
				transition, environment, environment.Failure, logReads.Load())
		}
	}
}

func TestDescribeFailureRedactsAndBounds(t *testing.T) {
	t.Parallel()
	described, _ := describe(t, []buildLogLine{
		{Level: "error", Output: ""},
		{Level: "error", Output: "Error: provider rejected the request: password=hunter2-very-secret " + strings.Repeat("word ", 200)},
	}, true)
	if described == nil || strings.Contains(described.Message, "hunter2") {
		t.Fatalf("message leaked a secret: %+v", described)
	}
	if !strings.HasSuffix(described.Message, "...") || len(described.Message) > len("The workspace failed to build: ")+buildFailureMessageLimit {
		t.Fatalf("message was not bounded: %d bytes", len(described.Message))
	}
}
