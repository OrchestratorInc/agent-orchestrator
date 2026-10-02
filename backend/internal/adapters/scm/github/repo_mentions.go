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

// The calls in this file back mention-spawn: an "@ao" comment under a pull
// request no AO session owns yet. FetchMentions cannot find those — it reads
// the timeline of a PR the observer already tracks — so these start from the
// repository instead.

// ListRepoMentions reads PR-timeline comments posted anywhere in repo since the
// given time and keeps the ones addressed to the agent.
//
// One repo-wide read replaces a timeline read per open PR: the cost stays at a
// single request per repository per poll however many PRs are open. GitHub's
// `since` filters on update time, so an old comment edited today comes back
// too; it is dropped here by its creation time, because editing a comment must
// not re-issue the instruction it carried.
func (p *Provider) ListRepoMentions(ctx context.Context, repo ports.SCMRepo, since time.Time) ([]ports.SCMRepoMention, error) {
	trigger := p.mentionTrigger()
	if trigger == "" {
		return nil, nil
	}
	matcher, err := mentionMatcher(trigger)
	if err != nil {
		return nil, fmt.Errorf("github scm: mention trigger %q is not usable: %w", trigger, err)
	}
	q := url.Values{}
	q.Set("per_page", "100")
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	q.Set("since", since.UTC().Format(time.RFC3339))
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "issues", "comments"), q, nil)
	if err != nil {
		return nil, err
	}
	var comments []restIssueComment
	if err := json.Unmarshal(resp.Body, &comments); err != nil {
		return nil, fmt.Errorf("github scm: repo comments malformed: %w", err)
	}
	var out []ports.SCMRepoMention
	for _, c := range comments {
		if c.CreatedAt.Before(since) || !addressedToAgent(c, matcher) {
			continue
		}
		number, prURL, ok := pullFromComment(c)
		if !ok {
			// A comment under a plain issue: issues are intake's business.
			continue
		}
		out = append(out, ports.SCMRepoMention{PRNumber: number, PRURL: prURL, Mention: c.mention()})
	}
	return out, nil
}

// pullFromComment tells a PR comment from an issue comment. Both live on the
// issues endpoint; only a PR comment's browser link points at /pull/.
func pullFromComment(c restIssueComment) (int, string, bool) {
	link, _, _ := strings.Cut(c.HTMLURL, "#")
	head, tail, ok := strings.Cut(link, "/pull/")
	if !ok || head == "" {
		return 0, "", false
	}
	number, err := strconv.Atoi(strings.Trim(tail, "/"))
	if err != nil || number <= 0 {
		return 0, "", false
	}
	return number, head + "/pull/" + strconv.Itoa(number), true
}

// FetchPullHead reads the facts needed to start work on someone else's PR: its
// state and where its head branch lives. The GraphQL batch the observer uses
// does not carry the head repository, and without it a fork PR — whose branch
// AO cannot push to — would look like any other.
func (p *Provider) FetchPullHead(ctx context.Context, ref ports.SCMPRRef) (ports.SCMPRObservation, error) {
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number)), nil, nil)
	if err != nil {
		return ports.SCMPRObservation{}, err
	}
	var pull restListPull
	if err := json.Unmarshal(resp.Body, &pull); err != nil {
		return ports.SCMPRObservation{}, fmt.Errorf("github scm: pull %d malformed: %w", ref.Number, err)
	}
	return restListPullToSCM(pull), nil
}

// CanPush reports whether login may push to repo — GitHub's write, maintain or
// admin role. It is the gate on who can start an agent from a comment: anyone
// who can comment on a public repo is not someone who should be able to spend
// agent time and deploy a branch.
func (p *Provider) CanPush(ctx context.Context, repo ports.SCMRepo, login string) (bool, error) {
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "collaborators", login, "permission"), nil, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Not a collaborator at all.
			return false, nil
		}
		return false, err
	}
	var body struct {
		Permission string `json:"permission"`
		RoleName   string `json:"role_name"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return false, fmt.Errorf("github scm: collaborator permission malformed: %w", err)
	}
	switch strings.ToLower(body.RoleName) {
	case "admin", "maintain", "write":
		return true, nil
	}
	switch strings.ToLower(body.Permission) {
	case "admin", "write":
		return true, nil
	}
	return false, nil
}
