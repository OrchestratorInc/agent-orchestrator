package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type fakePullRequestClient struct {
	raised []worker.RaisePullRequestRequest
}

func (f *fakePullRequestClient) pushGrant(context.Context) (worker.CheckoutGrantResponse, error) {
	return worker.CheckoutGrantResponse{Token: "t", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakePullRequestClient) raisePullRequest(_ context.Context, request worker.RaisePullRequestRequest) (worker.RaisePullRequestResponse, error) {
	f.raised = append(f.raised, request)
	return worker.RaisePullRequestResponse{Number: 7, HeadBranch: request.HeadBranch, BaseBranch: request.BaseBranch}, nil
}

func TestPullRequestBridgeOpensThePullRequestOnTheChosenRepository(t *testing.T) {
	var pushedFrom []string
	previous := pushBranch
	pushBranch = func(_ context.Context, _ worker.GitRunner, workspace, _ string, _ worker.CheckoutGrantResponse) error {
		pushedFrom = append(pushedFrom, workspace)
		return nil
	}
	t.Cleanup(func() { pushBranch = previous })
	launch := worker.LaunchContext{
		RepositoryURL: "https://github.com/acme/alice", DefaultBranch: "main",
		ExtraRepos: []worker.RepoRef{{URL: "https://github.com/acme/voice-calling-ai"}, {URL: "https://github.com/acme/julian", Branch: "develop"}},
	}
	workspace := "/home/dev/work/alice"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, test := range []struct {
		name, body                   string
		status                       int
		wantPath, wantRepo, wantBase string
	}{
		{"main repository", `{"branch":"ao/1","title":"t"}`, http.StatusOK, workspace, "", "main"},
		{"additional repository by name", `{"branch":"ao/1","title":"t","repository":"acme/voice-calling-ai"}`, http.StatusOK, "/home/dev/work/voice-calling-ai", "acme/voice-calling-ai", ""},
		{"additional repository by folder, cloned branch as base", `{"branch":"ao/1","title":"t","repository":"julian"}`, http.StatusOK, "/home/dev/work/julian", "acme/julian", "develop"},
		{"explicit base", `{"branch":"ao/1","title":"t","repository":"julian","base":"release"}`, http.StatusOK, "/home/dev/work/julian", "acme/julian", "release"},
		{"unknown repository", `{"branch":"ao/1","title":"t","repository":"acme/other"}`, http.StatusBadRequest, "", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			pushedFrom = nil
			client := &fakePullRequestClient{}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/pull-request", strings.NewReader(test.body))
			handlePullRequestBridgeRequest(recorder, request, client, workspace, launch, logger)
			if recorder.Code != test.status {
				t.Fatalf("status = %d (%s), want %d", recorder.Code, recorder.Body.String(), test.status)
			}
			if test.status != http.StatusOK {
				if len(pushedFrom) != 0 || len(client.raised) != 0 {
					t.Fatalf("refused request still pushed %v / raised %v", pushedFrom, client.raised)
				}
				return
			}
			if len(pushedFrom) != 1 || pushedFrom[0] != test.wantPath {
				t.Fatalf("pushed from %v, want %s", pushedFrom, test.wantPath)
			}
			if len(client.raised) != 1 || client.raised[0].Repository != test.wantRepo || client.raised[0].BaseBranch != test.wantBase {
				t.Fatalf("raised %+v, want repository %q base %q", client.raised, test.wantRepo, test.wantBase)
			}
			var response worker.RaisePullRequestResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Number != 7 {
				t.Fatalf("response %s", recorder.Body.String())
			}
		})
	}
}

func TestPullRequestHelpMentionsRepositoriesOnlyWhenThereAreSome(t *testing.T) {
	single := pullRequestHelp(worker.LaunchContext{RepositoryURL: "https://github.com/acme/alice"})
	if strings.Contains(single, `"repository"`) {
		t.Fatalf("single-repo help mentions repository: %s", single)
	}
	multi := pullRequestHelp(worker.LaunchContext{RepositoryURL: "https://github.com/acme/alice",
		ExtraRepos: []worker.RepoRef{{URL: "https://github.com/acme/julian"}}})
	if !strings.Contains(multi, `"repository":"<owner/name or folder name>"`) {
		t.Fatalf("multi-repo help lacks the repository field: %s", multi)
	}
}

// The bridge never pushes a repository's base branch: not the main
// repository's default branch, not the base a request names, and not an
// additional repository's own base.
func TestPullRequestBridgeRefusesToPushABaseBranch(t *testing.T) {
	pushed := false
	previousPush, previousBase := pushBranch, branchIsRepositoryBase
	pushBranch = func(context.Context, worker.GitRunner, string, string, worker.CheckoutGrantResponse) error {
		pushed = true
		return nil
	}
	branchIsRepositoryBase = func(_ context.Context, dir, _, branch string) bool {
		return dir == "/home/dev/work/voice-calling-ai" && branch == "trunk"
	}
	t.Cleanup(func() { pushBranch, branchIsRepositoryBase = previousPush, previousBase })
	launch := worker.LaunchContext{
		RepositoryURL: "https://github.com/acme/alice", DefaultBranch: "main",
		ExtraRepos: []worker.RepoRef{{URL: "https://github.com/acme/voice-calling-ai"}, {URL: "https://github.com/acme/julian", Branch: "develop"}},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, body := range []string{
		`{"branch":"main","title":"t"}`,
		`{"branch":"MAIN","title":"t"}`,
		`{"branch":"develop","title":"t","repository":"julian"}`,
		`{"branch":"release","title":"t","base":"release"}`,
		`{"branch":"trunk","title":"t","repository":"acme/voice-calling-ai"}`,
	} {
		pushed = false
		client := &fakePullRequestClient{}
		recorder := httptest.NewRecorder()
		handlePullRequestBridgeRequest(recorder, httptest.NewRequest(http.MethodPost, "/pull-request", strings.NewReader(body)),
			client, "/home/dev/work/alice", launch, logger)
		if recorder.Code != http.StatusBadRequest || pushed || len(client.raised) != 0 {
			t.Fatalf("%s: status %d pushed %v raised %d, want a refusal before any push", body, recorder.Code, pushed, len(client.raised))
		}
	}
}
