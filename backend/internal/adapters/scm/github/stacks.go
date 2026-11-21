package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMStacker = (*Provider)(nil)

// How long a stack merge is waited on, and the gaps between looks at it.
// Variables so tests don't sleep through them. The wait stays under the stack
// merge route's three-minute budget (httpd/api.go), which also covers the
// layers' readiness reads before the merge.
var (
	stackMergeWait      = 2 * time.Minute
	stackMergeFirstPoll = time.Second
	stackMergeMaxPoll   = 10 * time.Second
)

type restStack struct {
	Number int `json:"number"`
	Base   struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Open         bool `json:"open"`
	PullRequests []struct {
		Number   int     `json:"number"`
		State    string  `json:"state"`
		Draft    bool    `json:"draft"`
		MergedAt *string `json:"merged_at"`
		Head     struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_requests"`
}

func scmStack(repo ports.SCMRepo, in restStack) ports.SCMStack {
	host := strings.TrimSpace(repo.Host)
	if host == "" {
		host = "github.com"
	}
	stack := ports.SCMStack{
		Number:     in.Number,
		URL:        fmt.Sprintf("https://%s/%s/%s/stack/%d", host, repo.Owner, repo.Name, in.Number),
		BaseBranch: in.Base.Ref,
		Open:       in.Open,
		Layers:     make([]ports.SCMStackLayer, 0, len(in.PullRequests)),
	}
	for _, pr := range in.PullRequests {
		stack.Layers = append(stack.Layers, ports.SCMStackLayer{
			Number:     pr.Number,
			Open:       pr.State == "open",
			Draft:      pr.Draft,
			Merged:     pr.MergedAt != nil,
			HeadBranch: pr.Head.Ref,
			HeadSHA:    pr.Head.SHA,
		})
	}
	return stack
}

// PullRequestStack reads the stack holding ref. GitHub answers 404 where stacks
// don't exist, such as an Enterprise Server without them, which is not an error.
func (p *Provider) PullRequestStack(ctx context.Context, ref ports.SCMPRRef) (ports.SCMStack, bool, error) {
	if p == nil || p.client == nil {
		return ports.SCMStack{}, false, fmt.Errorf("github scm: stack provider is not configured")
	}
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(ref.Repo.Owner, ref.Repo.Name, "stacks"),
		url.Values{"pull_request": {strconv.Itoa(ref.Number)}}, nil)
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return ports.SCMStack{}, false, nil
		}
		return ports.SCMStack{}, false, err
	}
	var stacks []restStack
	if err := json.Unmarshal(resp.Body, &stacks); err != nil {
		return ports.SCMStack{}, false, fmt.Errorf("github scm: decode stacks: %w", err)
	}
	if len(stacks) == 0 {
		return ports.SCMStack{}, false, nil
	}
	return scmStack(ref.Repo, stacks[0]), true, nil
}

// FindOpenPullRequest looks a branch up in the base repository, where a stack's
// layers all live. Two open pull requests from one branch name no single parent.
func (p *Provider) FindOpenPullRequest(ctx context.Context, repo ports.SCMRepo, branch string) (ports.SCMPRRef, bool, error) {
	if p == nil || p.client == nil {
		return ports.SCMPRRef{}, false, fmt.Errorf("github scm: stack provider is not configured")
	}
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "pulls"), url.Values{
		"head":     {repo.Owner + ":" + branch},
		"state":    {"open"},
		"per_page": {"2"},
	}, nil)
	if err != nil {
		return ports.SCMPRRef{}, false, err
	}
	var pulls []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(resp.Body, &pulls); err != nil {
		return ports.SCMPRRef{}, false, fmt.Errorf("github scm: decode pull requests: %w", err)
	}
	if len(pulls) != 1 {
		return ports.SCMPRRef{}, false, nil
	}
	return ports.SCMPRRef{Repo: repo, Number: pulls[0].Number, URL: pulls[0].HTMLURL}, true, nil
}

// RegisterStacked starts a stack from Parent and Child, or adds Child to the
// stack Parent tops. GitHub checks that each base follows the head below it.
func (p *Provider) RegisterStacked(ctx context.Context, request ports.SCMStackRegisterRequest) (ports.SCMStack, error) {
	if p == nil || p.client == nil {
		return ports.SCMStack{}, fmt.Errorf("github scm: stack provider is not configured")
	}
	repo := request.Repo
	parent, inStack, err := p.PullRequestStack(ctx, ports.SCMPRRef{Repo: repo, Number: request.Parent})
	if err != nil {
		return ports.SCMStack{}, err
	}
	path := repoPath(repo.Owner, repo.Name, "stacks")
	numbers := []int{request.Parent, request.Child}
	if inStack {
		path = repoPath(repo.Owner, repo.Name, "stacks", strconv.Itoa(parent.Number), "add")
		numbers = []int{request.Child}
	}
	resp, err := p.client.doREST(ctx, http.MethodPost, path, nil, struct {
		PullRequests []int `json:"pull_requests"`
	}{PullRequests: numbers})
	if err != nil {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return ports.SCMStack{}, fmt.Errorf("%w: %w", ports.ErrSCMUnsupported, err)
		case http.StatusConflict, http.StatusUnprocessableEntity:
			return ports.SCMStack{}, fmt.Errorf("%w: %w", ports.ErrSCMRejected, err)
		default:
			return ports.SCMStack{}, err
		}
	}
	var stack restStack
	if err := json.Unmarshal(resp.Body, &stack); err != nil {
		return ports.SCMStack{}, fmt.Errorf("github scm: decode stack: %w", err)
	}
	return scmStack(repo, stack), nil
}

type asyncMerge struct {
	Status  string `json:"status"`
	Details struct {
		UUID    string `json:"uuid"`
		Message string `json:"message"`
	} `json:"details"`
}

// MergeStack starts GitHub's asynchronous merge, the only way to land a stack:
// it takes PR and every unmerged layer below it, all or nothing, and joins a
// merge queue if the base has one. Plain PUT /merge would land a middle layer
// on its parent's branch instead of the stack's base.
func (p *Provider) MergeStack(ctx context.Context, request ports.SCMStackMergeRequest) (ports.SCMStackMergeStatus, error) {
	if p == nil || p.client == nil {
		return "", fmt.Errorf("github scm: stack provider is not configured")
	}
	if !request.Method.Valid() {
		return "", fmt.Errorf("github scm: unsupported merge method %q", request.Method)
	}
	expectedHead := strings.TrimSpace(request.ExpectedHeadSHA)
	if !mergeHeadSHAPattern.MatchString(expectedHead) {
		return "", fmt.Errorf("github scm: invalid expected head sha")
	}
	repo, number := request.PR.Repo, strconv.Itoa(request.PR.Number)
	path := repoPath(repo.Owner, repo.Name, "pulls", number, "merge-async")
	resp, err := p.client.doREST(ctx, http.MethodPut, path, nil, struct {
		MergeMethod string `json:"merge_method"`
		MergeAction string `json:"merge_action"`
		SHA         string `json:"sha"`
	}{MergeMethod: string(request.Method), MergeAction: "default", SHA: expectedHead})
	if err != nil {
		return "", stackMergeError(resp.StatusCode, err)
	}
	result, err := decodeAsyncMerge(resp.Body)
	if err != nil {
		return "", err
	}

	deadline := time.Now().Add(stackMergeWait)
	delay := stackMergeFirstPoll
	for result.Status == "pending" && time.Now().Before(deadline) {
		if result.Details.UUID == "" {
			return "", errors.New("github scm: stack merge has no id to follow")
		}
		select {
		case <-ctx.Done():
			return ports.SCMStackPending, nil
		case <-time.After(delay):
		}
		delay = min(delay*2, stackMergeMaxPoll)
		// A look that fails says nothing about the merge, so the next one decides.
		poll, err := p.client.doREST(ctx, http.MethodGet, path+"/"+url.PathEscape(result.Details.UUID), nil, nil)
		if err != nil {
			continue
		}
		next, err := decodeAsyncMerge(poll.Body)
		if err != nil {
			continue
		}
		if next.Details.UUID == "" {
			next.Details.UUID = result.Details.UUID
		}
		result = next
	}
	switch result.Status {
	case "merged":
		return ports.SCMStackMerged, nil
	case "enqueued":
		return ports.SCMStackEnqueued, nil
	case "pending":
		return ports.SCMStackPending, nil
	default:
		return "", fmt.Errorf("%w: %s", ports.ErrSCMNotMergeable, strings.TrimSpace(result.Details.Message))
	}
}

func decodeAsyncMerge(body []byte) (asyncMerge, error) {
	var result asyncMerge
	if err := json.Unmarshal(body, &result); err != nil {
		return asyncMerge{}, fmt.Errorf("github scm: decode stack merge: %w", err)
	}
	return result, nil
}

func stackMergeError(status int, err error) error {
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
	case http.StatusConflict:
		return fmt.Errorf("%w: %w", ports.ErrSCMHeadChanged, err)
	case http.StatusMethodNotAllowed, http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %w", ports.ErrSCMNotMergeable, err)
	default:
		return err
	}
}
