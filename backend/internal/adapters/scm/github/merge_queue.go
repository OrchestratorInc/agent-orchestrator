package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMMergeQueuer = (*Provider)(nil)

// mergeQueueEntryFields is what AO reads of a MergeQueueEntry, in the batch
// query and in the enqueue mutation's payload.
const mergeQueueEntryFields = `state position estimatedTimeToMerge enqueuedAt`

// mergeQueueEntryFromGraphQL normalizes a PullRequest.mergeQueueEntry or a
// mutation payload's entry; nil when the PR isn't queued.
func mergeQueueEntryFromGraphQL(v any) *domain.MergeQueueEntry {
	entry, _ := v.(map[string]any)
	state := strings.ToLower(strings.TrimSpace(str(entry["state"])))
	if state == "" {
		return nil
	}
	return &domain.MergeQueueEntry{
		State:                   domain.MergeQueueState(state),
		Position:                int(num(entry["position"])),
		EstimatedSecondsToMerge: int(num(entry["estimatedTimeToMerge"])),
		EnqueuedAt:              parseGitHubTime(str(entry["enqueuedAt"])),
	}
}

// EnqueuePullRequest adds a pull request to its base branch's merge queue,
// the only way to merge on a branch that requires one: the REST merge
// endpoint doesn't merge through a queue. expectedHeadOid makes it a
// compare-and-swap on the reviewed head, as the sha field does for a merge.
// GitHub adds it only when it can join at once; otherwise the mutation fails
// with why, and nothing is left armed to join later.
func (p *Provider) EnqueuePullRequest(ctx context.Context, request ports.SCMEnqueueRequest) (domain.MergeQueueEntry, error) {
	if p == nil || p.client == nil {
		return domain.MergeQueueEntry{}, fmt.Errorf("github scm: merge queue provider is not configured")
	}
	expectedHead := strings.TrimSpace(request.ExpectedHeadSHA)
	if !mergeHeadSHAPattern.MatchString(expectedHead) {
		return domain.MergeQueueEntry{}, fmt.Errorf("github scm: invalid expected head sha")
	}
	nodeID, err := p.mergeQueueNodeID(ctx, request.PR, request.NodeID)
	if err != nil {
		return domain.MergeQueueEntry{}, err
	}
	data, err := p.client.doGraphQL(ctx, `mutation EnqueuePullRequest($id: ID!, $head: GitObjectID!) {
  enqueuePullRequest(input: { pullRequestId: $id, expectedHeadOid: $head }) { mergeQueueEntry { `+mergeQueueEntryFields+` } }
}`, map[string]any{"id": nodeID, "head": expectedHead})
	if err != nil {
		return domain.MergeQueueEntry{}, mapMergeQueueError(err)
	}
	payload, _ := data["enqueuePullRequest"].(map[string]any)
	entry := mergeQueueEntryFromGraphQL(payload["mergeQueueEntry"])
	if entry == nil {
		return domain.MergeQueueEntry{}, fmt.Errorf("%w: enqueuePullRequest returned no queue entry", ports.ErrSCMRejected)
	}
	return *entry, nil
}

// DequeuePullRequest removes a pull request from its merge queue.
func (p *Provider) DequeuePullRequest(ctx context.Context, request ports.SCMDequeueRequest) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github scm: merge queue provider is not configured")
	}
	nodeID, err := p.mergeQueueNodeID(ctx, request.PR, request.NodeID)
	if err != nil {
		return err
	}
	// The input's id is the pull request's node id, not the entry's.
	if _, err := p.client.doGraphQL(ctx, `mutation DequeuePullRequest($id: ID!) {
  dequeuePullRequest(input: { id: $id }) { clientMutationId }
}`, map[string]any{"id": nodeID}); err != nil {
		return mapMergeQueueError(err)
	}
	return nil
}

func (p *Provider) mergeQueueNodeID(ctx context.Context, ref ports.SCMPRRef, nodeID string) (string, error) {
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" {
		return "", fmt.Errorf("github scm: invalid pull request reference")
	}
	if id := strings.TrimSpace(nodeID); id != "" {
		return id, nil
	}
	id, err := p.pullRequestNodeID(ctx, ref)
	if errors.Is(err, ErrNotFound) {
		return "", fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
	}
	return id, err
}

// mapMergeQueueError classifies a failed queue mutation. GitHub reports a head
// that moved past expectedHeadOid, and every other refusal (requirements not
// met, already queued, no queue on the branch), as GraphQL errors.
func mapMergeQueueError(err error) error {
	if errors.Is(err, errGraphQL) {
		// GitHub's wording for a moved head on its merge mutations; the
		// enqueue docs don't give one, so anything else stays a refusal.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "head branch was modified") || strings.Contains(msg, "expected head") {
			return fmt.Errorf("%w: %w", ports.ErrSCMHeadChanged, err)
		}
	}
	return mapGraphQLMutationError(err)
}
