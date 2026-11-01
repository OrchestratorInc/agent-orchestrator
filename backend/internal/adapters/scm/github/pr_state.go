package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMPRStateChanger = (*Provider)(nil)

// ChangePullRequestState closes, reopens, or toggles the draft state of a
// GitHub pull request. Close and reopen use the REST state field; GitHub only
// exposes draft toggling through GraphQL, which addresses the PR by node id.
func (p *Provider) ChangePullRequestState(ctx context.Context, request ports.SCMPRStateRequest) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github scm: state provider is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return fmt.Errorf("github scm: invalid pull request reference")
	}
	switch request.Action {
	case ports.SCMPRClose:
		return p.setPullRequestRESTState(ctx, request.PR, "closed")
	case ports.SCMPRReopen:
		return p.setPullRequestRESTState(ctx, request.PR, "open")
	case ports.SCMPRReady, ports.SCMPRDraft:
		return p.setPullRequestDraft(ctx, request)
	default:
		return fmt.Errorf("%w: pull request state action %q", ports.ErrSCMUnsupported, request.Action)
	}
}

func (p *Provider) setPullRequestRESTState(ctx context.Context, ref ports.SCMPRRef, state string) error {
	payload := struct {
		State string `json:"state"`
	}{State: state}
	resp, err := p.client.doREST(ctx, http.MethodPatch,
		repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number)), nil, payload)
	if err != nil {
		return mapMutationStatus(resp.StatusCode, err)
	}
	var result struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return fmt.Errorf("github scm: decode pull request state response: %w", err)
	}
	if !strings.EqualFold(result.State, state) {
		return fmt.Errorf("%w: pull request state is %q, want %q", ports.ErrSCMRejected, result.State, state)
	}
	return nil
}

func (p *Provider) setPullRequestDraft(ctx context.Context, request ports.SCMPRStateRequest) error {
	nodeID := strings.TrimSpace(request.NodeID)
	if nodeID == "" {
		var err error
		if nodeID, err = p.pullRequestNodeID(ctx, request.PR); err != nil {
			return err
		}
	}
	field, wantDraft := "markPullRequestReadyForReview", false
	if request.Action == ports.SCMPRDraft {
		field, wantDraft = "convertPullRequestToDraft", true
	}
	query := fmt.Sprintf(`mutation ChangePullRequestDraft($id: ID!) {
  %s(input: { pullRequestId: $id }) { pullRequest { id isDraft } }
}`, field)
	data, err := p.client.doGraphQL(ctx, query, map[string]any{"id": nodeID})
	if err != nil {
		return mapGraphQLMutationError(err)
	}
	mutation, _ := data[field].(map[string]any)
	pr, _ := mutation["pullRequest"].(map[string]any)
	if pr == nil {
		return fmt.Errorf("github scm: %s returned no pull request", field)
	}
	if returned := str(pr["id"]); returned != nodeID {
		return fmt.Errorf("github scm: %s returned unexpected pull request %q", field, returned)
	}
	isDraft, ok := pr["isDraft"].(bool)
	if !ok || isDraft != wantDraft {
		return fmt.Errorf("%w: %s was not confirmed", ports.ErrSCMRejected, field)
	}
	return nil
}

// pullRequestNodeID looks up the GraphQL node id that draft mutations address.
func (p *Provider) pullRequestNodeID(ctx context.Context, ref ports.SCMPRRef) (string, error) {
	data, err := p.client.doGraphQL(ctx, `query PullRequestNodeID($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { id } }
}`, map[string]any{"owner": ref.Repo.Owner, "name": ref.Repo.Name, "number": ref.Number})
	if err != nil {
		return "", err
	}
	repo, _ := data["repository"].(map[string]any)
	pr, _ := repo["pullRequest"].(map[string]any)
	id := strings.TrimSpace(str(pr["id"]))
	if id == "" {
		return "", fmt.Errorf("%w: pull request %s#%d", ErrNotFound, repoFullName(ref.Repo), ref.Number)
	}
	return id, nil
}

// mapMutationStatus classifies a failed REST mutation: 404 is an unknown PR
// and 422 (or 403 that is not an auth failure) is GitHub refusing it.
func mapMutationStatus(status int, err error) error {
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
	case http.StatusUnprocessableEntity, http.StatusConflict, http.StatusMethodNotAllowed:
		return fmt.Errorf("%w: %w", ports.ErrSCMRejected, err)
	default:
		return err
	}
}

// mapGraphQLMutationError treats a plain GraphQL error payload on a mutation
// as the provider refusing it; auth, rate-limit, and not-found failures keep
// their own classification.
func mapGraphQLMutationError(err error) error {
	if errors.Is(err, errGraphQL) {
		return fmt.Errorf("%w: %w", ports.ErrSCMRejected, err)
	}
	return err
}
