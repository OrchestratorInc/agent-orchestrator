package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const maxPullRequestBridgeBody = 1 << 16

type pullRequestBridgeRequest struct {
	Branch string `json:"branch"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Base   string `json:"base"`
	// Repository picks the project repository to open the pull request on:
	// its GitHub owner/name or its checkout's folder name. Empty is the main
	// repository.
	Repository string `json:"repository"`
}

func runPullRequestBridge(
	ctx context.Context,
	socketPath string,
	apiClient *client,
	workspace string,
	launch worker.LaunchContext,
	logger *slog.Logger,
) error {
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on pull request bridge socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("secure pull request bridge socket: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pull-request", func(w http.ResponseWriter, r *http.Request) {
		handlePullRequestBridgeRequest(w, r, apiClient, workspace, launch, logger)
	})
	server := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func handlePullRequestBridgeRequest(
	w http.ResponseWriter,
	r *http.Request,
	apiClient pullRequestBridgeClient,
	workspace string,
	launch worker.LaunchContext,
	logger *slog.Logger,
) {
	var input pullRequestBridgeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPullRequestBridgeBody)).Decode(&input); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input.Branch = strings.TrimSpace(input.Branch)
	input.Title = strings.TrimSpace(input.Title)
	input.Base = strings.TrimSpace(input.Base)
	if input.Branch == "" || input.Title == "" {
		writeBridgeError(w, http.StatusBadRequest, "branch and title are required")
		return
	}
	checkout, err := worker.ResolveRepoCheckout(
		worker.SessionRepoCheckouts(workspace, launch.RepositoryURL, launch.ExtraRepos), input.Repository)
	if err != nil {
		writeBridgeError(w, http.StatusBadRequest, "repository must be one of this session's repositories (owner/name or folder name)")
		return
	}
	if input.Base == "" {
		// The main repository's base is the project's default branch. An
		// additional repository's is the branch it was cloned at, or, when
		// empty, its own default branch, which the control plane resolves.
		input.Base = strings.TrimSpace(launch.DefaultBranch)
		if !checkout.Main {
			input.Base = checkout.Branch
		}
	}
	repository := ""
	if !checkout.Main {
		repository = checkout.FullName
	}
	ctx := r.Context()
	// The branch must be the session's work, never a repository's base: a push
	// of HEAD to the base would land the work there directly.
	if strings.EqualFold(input.Branch, input.Base) ||
		(checkout.Main && (strings.EqualFold(input.Branch, strings.TrimSpace(launch.DefaultBranch)) ||
			branchIsRepositoryBase(ctx, checkout.Path, "", input.Branch))) ||
		(!checkout.Main && branchIsRepositoryBase(ctx, checkout.Path, checkout.Branch, input.Branch)) {
		writeBridgeError(w, http.StatusBadRequest, "refusing to push "+input.Branch+": it is the base branch of "+checkout.Name+"; commit on the session branch instead")
		return
	}
	grant, err := apiClient.pushGrant(ctx)
	if err != nil {
		logger.Error("pull request bridge: push grant failed", "error", err)
		writeBridgeError(w, http.StatusBadGateway, "could not obtain a push grant")
		return
	}
	if err := pushBranch(ctx, worker.ExecGitRunner{}, checkout.Path, input.Branch, grant); err != nil {
		logger.Error("pull request bridge: push failed", "error", err)
		writeBridgeError(w, http.StatusBadGateway, "push failed: "+err.Error())
		return
	}
	response, err := apiClient.raisePullRequest(ctx, worker.RaisePullRequestRequest{
		Title:      input.Title,
		Body:       input.Body,
		HeadBranch: input.Branch,
		BaseBranch: input.Base,
		Repository: repository,
	})
	if err != nil {
		logger.Error("pull request bridge: raise pull request failed", "error", err)
		writeBridgeError(w, http.StatusBadGateway, "pull request could not be opened")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// pullRequestHelp is AO_PULL_REQUEST_HELP: how the agent opens a pull request
// through the bridge, and, in a session with additional repositories, how it
// picks the repository.
func pullRequestHelp(launch worker.LaunchContext) string {
	help := "curl --unix-socket $AO_PULL_REQUEST_SOCKET " +
		`-X POST http://localhost/pull-request -H 'Content-Type: application/json' ` +
		`-d '{"branch":"<pushed branch name>","title":"<PR title>","body":"<PR body>"}' ` +
		"to push the current branch and open a pull request against the project's configured default branch."
	if len(launch.ExtraRepos) == 0 || worker.IsScratchRepositoryURL(launch.RepositoryURL) {
		return help
	}
	return help + ` For one of the additional repositories add "repository":"<owner/name or folder name>" to push that ` +
		"repository's current branch and open the pull request on it, against its own default branch unless you pass \"base\". " +
		"Open one pull request per repository you changed and mention the related pull requests in each body."
}

// pullRequestBridgeClient is the control-plane surface the bridge uses.
type pullRequestBridgeClient interface {
	pushGrant(context.Context) (worker.CheckoutGrantResponse, error)
	raisePullRequest(context.Context, worker.RaisePullRequestRequest) (worker.RaisePullRequestResponse, error)
}

// pushBranch is worker.PushBranch; tests replace it.
var pushBranch = worker.PushBranch

// branchIsRepositoryBase is worker.SessionBranchIsBase on the checkout at dir;
// tests replace it.
var branchIsRepositoryBase = func(ctx context.Context, dir, configured, branch string) bool {
	return worker.SessionBranchIsBase(ctx, worker.ExecGitRunner{}, dir, configured, branch)
}

func writeBridgeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
