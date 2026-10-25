package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// pullRequestSocketEnv names the worker's pull request bridge socket.
const pullRequestSocketEnv = "AO_PULL_REQUEST_SOCKET"

// pullRequestCreateRequest is the bridge's request (see ao-worker's
// pullRequestBridgeRequest).
type pullRequestCreateRequest struct {
	Branch     string `json:"branch"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Base       string `json:"base,omitempty"`
	Repository string `json:"repository,omitempty"`
	Draft      bool   `json:"draft,omitempty"`
}

// runPullRequestCreate is `gh pr create` for a cloud session: it takes gh's
// flags, pushes the checked-out branch and opens its pull request through the
// worker's bridge, so the control plane opens it as the session's owner and the
// session shows it at once. It prints the pull request's URL, as gh does.
func runPullRequestCreate(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("pr-create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var title, body, bodyFile, base, head, repo string
	var draft, fill bool
	for _, name := range []string{"title", "t"} {
		flags.StringVar(&title, name, "", "pull request title")
	}
	for _, name := range []string{"body", "b"} {
		flags.StringVar(&body, name, "", "pull request body")
	}
	for _, name := range []string{"body-file", "F"} {
		flags.StringVar(&bodyFile, name, "", "read the body from a file (- for standard input)")
	}
	for _, name := range []string{"base", "B"} {
		flags.StringVar(&base, name, "", "branch to merge into")
	}
	for _, name := range []string{"head", "H"} {
		flags.StringVar(&head, name, "", "branch with the changes (the checked-out branch)")
	}
	for _, name := range []string{"repo", "R"} {
		flags.StringVar(&repo, name, "", "OWNER/REPO of one of the session's repositories")
	}
	for _, name := range []string{"draft", "d"} {
		flags.BoolVar(&draft, name, false, "open as a draft")
	}
	for _, name := range []string{"fill", "f", "fill-first", "fill-verbose"} {
		flags.BoolVar(&fill, name, false, "use the last commit's message for the title and body")
	}
	var ignored []string
	for _, name := range []string{"assignee", "a", "reviewer", "r", "label", "l", "milestone", "m", "project", "p", "template", "T"} {
		flags.Func(name, "not supported in a cloud session; ignored", func(string) error {
			ignored = append(ignored, "--"+name)
			return nil
		})
	}
	flags.BoolFunc("no-maintainer-edit", "ignored", func(string) error { return nil })
	for _, name := range []string{"web", "w", "editor", "e", "dry-run"} {
		flags.BoolFunc(name, "not supported in a cloud session", func(string) error {
			return fmt.Errorf("--%s is not supported in an AO cloud session; pass --title and --body", name)
		})
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	branch, err := gitOutput(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" || branch == "HEAD" {
		return errors.New("check out the branch to open a pull request for; HEAD is not on a branch")
	}
	if head = strings.TrimSpace(head); head != "" && head != branch {
		// The bridge pushes the checked-out branch, so a different --head
		// would push this branch's commits there.
		return fmt.Errorf("--head %s is not the checked-out branch %s; switch to it first", head, branch)
	}
	if bodyFile != "" {
		content, err := readBodyFile(bodyFile, stdin)
		if err != nil {
			return err
		}
		body = content
	}
	if fill {
		if strings.TrimSpace(title) == "" {
			title, _ = gitOutput(ctx, "log", "-1", "--format=%s")
		}
		if body == "" {
			body, _ = gitOutput(ctx, "log", "-1", "--format=%b")
		}
	}
	if strings.TrimSpace(title) == "" {
		return errors.New("must provide --title (or --fill) when not running interactively")
	}
	if strings.TrimSpace(repo) == "" {
		// The repository the command runs in: one of the session's additional
		// repositories opens the pull request there.
		if origin, err := gitOutput(ctx, "config", "--get", "remote.origin.url"); err == nil {
			repo, _ = githubFullName(origin)
		}
	}
	socket := strings.TrimSpace(os.Getenv(pullRequestSocketEnv))
	if socket == "" {
		return errors.New("ao pr-create must run inside an AO Cloud worker")
	}
	for _, name := range ignored {
		fmt.Fprintf(stderr, "ao: %s is not supported in a cloud session and was ignored\n", name)
	}
	response, err := postPullRequestBridge(ctx, socket, pullRequestCreateRequest{
		Branch: branch, Title: strings.TrimSpace(title), Body: body,
		Base: strings.TrimSpace(base), Repository: strings.TrimSpace(repo), Draft: draft,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, response.HTMLURL)
	return nil
}

func readBodyFile(path string, stdin io.Reader) (string, error) {
	var reader io.Reader = stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read --body-file: %w", err)
		}
		defer file.Close()
		reader = file
	}
	content, err := io.ReadAll(io.LimitReader(reader, 60<<10))
	if err != nil {
		return "", fmt.Errorf("read --body-file: %w", err)
	}
	return string(content), nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "git", args...).Output()
	return strings.TrimSpace(string(output)), err
}

// postPullRequestBridge sends the request to the worker's bridge socket. A
// push and the pull request call can take a while on a large branch.
func postPullRequestBridge(ctx context.Context, socket string, input pullRequestCreateRequest) (worker.RaisePullRequestResponse, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return worker.RaisePullRequestResponse{}, err
	}
	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/pull-request", bytes.NewReader(payload))
	if err != nil {
		return worker.RaisePullRequestResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return worker.RaisePullRequestResponse{}, fmt.Errorf("reach the worker's pull request bridge: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return worker.RaisePullRequestResponse{}, err
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(content, &failure) == nil && failure.Error != "" {
			return worker.RaisePullRequestResponse{}, errors.New(failure.Error)
		}
		return worker.RaisePullRequestResponse{}, fmt.Errorf("the pull request bridge answered %s", response.Status)
	}
	var created worker.RaisePullRequestResponse
	if err := json.Unmarshal(content, &created); err != nil || created.HTMLURL == "" {
		return worker.RaisePullRequestResponse{}, errors.New("the pull request bridge returned an incomplete response")
	}
	return created, nil
}
