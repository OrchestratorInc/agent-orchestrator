package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultMentionTrigger is the phrase a PR-timeline comment must contain before
// it is delivered to the agent. Anything else is people talking to each other.
const DefaultMentionTrigger = "@ao"

// mentionWindow bounds how far back timeline comments are read. GitHub returns
// issue comments oldest-first, so an unbounded read on a long thread would page
// through history and still miss today's mention; asking for a recent window
// keeps the newest comments on the first page. A day is far longer than any
// session waits for a reply.
const mentionWindow = 24 * time.Hour

// restIssueComment is the subset of GitHub's issue-comment payload AO reads.
// PR-timeline comments live on the issues endpoint: the pulls endpoint serves
// review comments, which are a different thing entirely.
type restIssueComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	// IssueURL is the API URL of the issue or PR the comment belongs to; the
	// repo-wide scan reads the number from it.
	IssueURL string `json:"issue_url"`
	User     struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

// FetchMentions reads recent PR-timeline comments and keeps the ones addressed
// to the agent.
//
// It is deliberately its own provider call rather than a side effect of the
// review poll: review threads are only re-read when the review decision changes,
// so a PR nobody has formally reviewed — the common case — would never be
// scanned for comments at all.
func (p *Provider) FetchMentions(ctx context.Context, ref ports.SCMPRRef) ([]ports.SCMMentionObservation, error) {
	return p.fetchMentions(ctx, ref, time.Now()), nil
}

func (p *Provider) fetchMentions(ctx context.Context, ref ports.SCMPRRef, now time.Time) []ports.SCMMentionObservation {
	trigger := p.mentionTrigger()
	if trigger == "" {
		return nil
	}
	q := url.Values{}
	q.Set("per_page", "100")
	q.Set("since", now.Add(-mentionWindow).UTC().Format(time.RFC3339))
	path := repoPath(ref.Repo.Owner, ref.Repo.Name, "issues", strconv.Itoa(ref.Number), "comments")

	resp, err := p.client.doREST(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		p.logger.Warn("github scm: timeline comments unreadable, mentions skipped this poll",
			"repo", repoFullName(ref.Repo), "pr", ref.Number, "err", err)
		return nil
	}
	var comments []restIssueComment
	if err := json.Unmarshal(resp.Body, &comments); err != nil {
		p.logger.Warn("github scm: timeline comments malformed",
			"repo", repoFullName(ref.Repo), "pr", ref.Number, "err", err)
		return nil
	}

	matcher, err := mentionMatcher(trigger)
	if err != nil {
		p.logger.Warn("github scm: mention trigger is not usable", "trigger", trigger, "err", err)
		return nil
	}

	out := make([]ports.SCMMentionObservation, 0, len(comments))
	for _, c := range comments {
		if !addressedToAgent(c, matcher) {
			continue
		}
		out = append(out, c.mention())
	}
	return out
}

// addressedToAgent is the one rule for "this comment is an instruction for
// the agent", shared by the per-PR timeline read and the repo-wide scan.
func addressedToAgent(c restIssueComment, matcher *regexp.Regexp) bool {
	// Bot comments include AO's own replies and CI chatter; echoing those
	// back to the agent would loop.
	if strings.EqualFold(c.User.Type, "Bot") {
		return false
	}
	return matcher.MatchString(c.Body)
}

func (c restIssueComment) mention() ports.SCMMentionObservation {
	return ports.SCMMentionObservation{
		ID:        strconv.FormatInt(c.ID, 10),
		Author:    c.User.Login,
		Body:      c.Body,
		URL:       c.HTMLURL,
		CreatedAt: c.CreatedAt,
	}
}

// mentionTrigger returns the configured phrase, defaulting to "@ao".
func (p *Provider) mentionTrigger() string {
	if trimmed := strings.TrimSpace(p.mentionTriggerPhrase); trimmed != "" {
		return trimmed
	}
	return DefaultMentionTrigger
}

// mentionMatcher builds the trigger test: case-insensitive, and bounded so
// "@aoagents" or an email-looking "x@ao" does not count as addressing the agent.
func mentionMatcher(trigger string) (*regexp.Regexp, error) {
	return regexp.Compile(fmt.Sprintf(`(?i)(^|[^\w@])%s($|[^\w-])`, regexp.QuoteMeta(trigger)))
}
