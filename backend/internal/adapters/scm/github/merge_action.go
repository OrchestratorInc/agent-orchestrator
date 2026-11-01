package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var mergeHeadSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

var _ ports.SCMMerger = (*Provider)(nil)

// MergePullRequest performs a GitHub squash, merge-commit, or rebase merge
// guarded by the reviewed head SHA. GitHub's sha field is a compare-and-swap
// precondition.
func (p *Provider) MergePullRequest(ctx context.Context, request ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMMergeResult{}, fmt.Errorf("github scm: merge provider is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return ports.SCMMergeResult{}, fmt.Errorf("github scm: invalid pull request reference")
	}
	if !request.Method.Valid() {
		return ports.SCMMergeResult{}, fmt.Errorf("github scm: unsupported merge method %q", request.Method)
	}
	expectedHead := strings.TrimSpace(request.ExpectedHeadSHA)
	if !mergeHeadSHAPattern.MatchString(expectedHead) {
		return ports.SCMMergeResult{}, fmt.Errorf("github scm: invalid expected head sha")
	}

	payload := struct {
		SHA         string `json:"sha"`
		MergeMethod string `json:"merge_method"`
	}{SHA: expectedHead, MergeMethod: string(request.Method)}
	resp, err := p.client.doREST(ctx, http.MethodPut,
		repoPath(request.PR.Repo.Owner, request.PR.Repo.Name, "pulls", strconv.Itoa(request.PR.Number), "merge"),
		nil, payload)
	if err != nil {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return ports.SCMMergeResult{}, fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
		case http.StatusConflict:
			return ports.SCMMergeResult{}, fmt.Errorf("%w: %w", ports.ErrSCMHeadChanged, err)
		case http.StatusMethodNotAllowed, http.StatusUnprocessableEntity:
			return ports.SCMMergeResult{}, fmt.Errorf("%w: %w", ports.ErrSCMNotMergeable, err)
		default:
			return ports.SCMMergeResult{}, err
		}
	}

	var result struct {
		SHA     string `json:"sha"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return ports.SCMMergeResult{}, fmt.Errorf("github scm: decode merge response: %w", err)
	}
	if !result.Merged {
		return ports.SCMMergeResult{}, fmt.Errorf("%w: %s", ports.ErrSCMNotMergeable, strings.TrimSpace(result.Message))
	}
	return ports.SCMMergeResult{MergeCommitSHA: result.SHA}, nil
}
