package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

const reviewMaxNudge = 3

type reactionState struct {
	mu           sync.Mutex
	seen         map[string]string
	attempts     map[string]int
	loaded       map[string]bool
	pendingRearm map[string]bool
}

func newReactionState() reactionState {
	return reactionState{seen: map[string]string{}, attempts: map[string]int{}, loaded: map[string]bool{}, pendingRearm: map[string]bool{}}
}

type reactionPayload struct {
	Seen     map[string]string `json:"seen,omitempty"`
	Attempts map[string]int    `json:"attempts,omitempty"`
}

type pendingNudge struct {
	key         string
	sig         string
	msg         string
	maxAttempts int
	urgent      bool
}

func (m *Manager) ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	if err := m.ReconcileSessionOutputType(ctx, id); err != nil {
		slog.Default().Warn("lifecycle: reconcile session output type", "session", id, "err", err)
	}
	if o.Merged || o.Closed {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || !ok {
			return err
		}
		if rec.IsTerminated || !rec.TerminateOnPRMerge {
			return nil
		}
		if rec.Activity.State == domain.ActivityActive {
			return nil
		}
		done, err := m.sessionComplete(ctx, id)
		if err != nil {
			return err
		}
		if done {
			return m.terminateCompletedSession(ctx, id)
		}
		return nil
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	var rearmErr error
	if mergeabilityClearsConflict(o.Mergeability) {
		rearmErr = m.rearmMergeConflict(ctx, o.URL)
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
		return rearmErr
	}
	needsInput := rec.Activity.State.NeedsInput()
	ident := prIdentity(o)
	var nudges []pendingNudge
	var ciPolicyErr error

	if !needsInput {
		reviews, err := m.store.ListPRReviews(ctx, o.URL)
		if err != nil {
			return fmt.Errorf("list persisted reviews for %s: %w", o.URL, err)
		}
		comments, err := m.store.ListPRComments(ctx, o.URL)
		if err != nil {
			return fmt.Errorf("list persisted comments for %s: %w", o.URL, err)
		}
		var prAuthor string
		if pr, ok, err := m.store.GetPR(ctx, o.URL); err == nil && ok {
			prAuthor = pr.Author
		}
		o.Comments = prCommentObservations(withoutPRAuthorReplies(comments, prAuthor))

		if o.CI == domain.CIFailing {
			pr, ok, err := m.store.GetPR(ctx, o.URL)
			switch {
			case err != nil:
				ciPolicyErr = fmt.Errorf("load CI injection policy for %s: %w", o.URL, err)
			case !ok:
				ciPolicyErr = fmt.Errorf("load CI injection policy for %s: PR not found", o.URL)
			case pr.AutoInjectCI:
				checks := failedPRChecks(o.Checks)
				if len(checks) > 0 {
					msg := formatCIFailureMessage(checks)
					if ident != "your PR" {
						msg = strings.Replace(msg, "your PR", ident, 1)
					}
					if o.URL != "" {
						msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
					}
					nudges = append(nudges, pendingNudge{key: "ci:" + o.URL, sig: ciFailureSignature(checks), msg: msg, maxAttempts: 0})
				}
			}
		}

		if hasUnresolvedComments(o.Comments) {
			comments := unresolvedReviewComments(o.Comments)
			for _, comment := range comments {
				if !comment.AutoInjectReview {
					continue
				}
				commentSlice := []ports.PRCommentObservation{comment}
				msg := formatReviewCommentsMessage(commentSlice)
				if ident != "your PR" {
					msg = strings.Replace(msg, "your PR", ident, 1)
				}
				if o.URL != "" {
					msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
				}
				sig := reviewCommentsSignature(commentSlice)
				if sig == "" {
					sig = string(o.Review)
				}
				nudges = append(nudges, pendingNudge{key: commentNudgeKey(o.URL, comment), sig: sig, msg: msg, maxAttempts: reviewMaxNudge})
			}
		}

		if o.Review == domain.ReviewChangesRequest {
			for _, review := range reviews {
				if review.State != domain.ReviewChangesRequest || !review.AutoInjectReview {
					continue
				}
				msg := formatReviewChangesRequestedMessage(review)
				if ident != "your PR" {
					msg = strings.Replace(msg, "your PR", ident, 1)
				}
				if o.URL != "" && review.URL == "" {
					msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
				}
				nudges = append(nudges, pendingNudge{key: "review:" + o.URL + ":" + review.ID, sig: string(review.State), msg: msg, maxAttempts: reviewMaxNudge})
			}
		}
	}

	var blockedCheckErr error
	if o.Mergeability == domain.MergeConflicting {
		pr, prFound, prErr := m.store.GetPR(ctx, o.URL)
		var skip bool
		var err error
		switch {
		case prErr != nil:
			err = fmt.Errorf("load CI injection policy for %s: %w", o.URL, prErr)
		case !prFound:
			err = fmt.Errorf("load CI injection policy for %s: PR not found", o.URL)
		case !pr.AutoInjectCI:
			skip = true
		default:
			skip, err = m.prBlockedByOpenParent(ctx, id, o.URL)
		}
		switch {
		case err != nil:
			blockedCheckErr = err
		case !skip:
			msg := "There are merge conflicts on " + ident + ". Rebase onto the base branch and resolve them."
			if o.URL != "" {
				msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
			}
			nudges = append(nudges, pendingNudge{key: mergeConflictKey(o.URL), sig: string(o.Mergeability), msg: msg, maxAttempts: 0, urgent: true})
		}
	}

	for _, n := range nudges {
		if _, err := m.sendOnce(ctx, id, o.URL, n.key, n.sig, n.msg, n.maxAttempts, n.urgent); err != nil {
			return err
		}
	}
	if ciPolicyErr != nil {
		return ciPolicyErr
	}
	if blockedCheckErr != nil {
		return blockedCheckErr
	}
	return rearmErr
}

func (m *Manager) terminateCompletedSession(ctx context.Context, id domain.SessionID) error {
	if m.sessionMutationInProgress(id) {
		return nil
	}
	m.mu.Lock()
	terminator := m.completionTerminator
	m.mu.Unlock()
	if terminator == nil {
		return fmt.Errorf("terminate completed session %s: session terminator is not configured", id)
	}
	before, live := m.liveBeforeEnd(ctx, id)
	_, err := terminator.Kill(ctx, id)
	m.reportEndIfTerminated(ctx, before, live, err, deliverytelemetry.EndPRMerged)
	if err != nil {
		return fmt.Errorf("terminate completed session %s: %w", id, err)
	}
	return nil
}

func (m *Manager) sessionComplete(ctx context.Context, id domain.SessionID) (bool, error) {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return false, err
	}
	merged := false
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			return false, nil
		}
		if pr.Merged {
			merged = true
		}
	}
	return merged, nil
}

func commentNudgeKey(prURL string, comment ports.PRCommentObservation) string {
	id := strings.TrimSpace(comment.ID)
	if id == "" {
		id = strings.TrimSpace(comment.ThreadID)
	}
	return "comment:" + prURL + ":" + id
}

func mergeConflictKey(prURL string) string { return "merge-conflict:" + prURL }

func mergeabilityClearsConflict(state domain.Mergeability) bool {
	return state == domain.MergeMergeable || state == domain.MergeUnstable
}

func (m *Manager) rearmMergeConflict(ctx context.Context, prURL string) error {
	if m.guard == nil || prURL == "" {
		return nil
	}
	m.react.mu.Lock()
	defer m.react.mu.Unlock()

	if !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return err
		}
		m.react.loaded[prURL] = true
	}
	key := mergeConflictKey(prURL)
	_, seen := m.react.seen[key]
	_, attempted := m.react.attempts[key]
	if !seen && !attempted && !m.react.pendingRearm[prURL] {
		return nil
	}
	delete(m.react.seen, key)
	delete(m.react.attempts, key)
	if err := m.persistPRSignaturesLocked(ctx, prURL); err != nil {
		m.react.pendingRearm[prURL] = true
		return err
	}
	delete(m.react.pendingRearm, prURL)
	return nil
}

func (m *Manager) prBlockedByOpenParent(ctx context.Context, id domain.SessionID, prURL string) (bool, error) {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return false, err
	}
	openSources := make(map[string]bool, len(prs))
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed && pr.SourceBranch != "" {
			openSources[pr.SourceBranch] = true
		}
	}
	for _, pr := range prs {
		if pr.URL == prURL {
			return pr.TargetBranch != "" && openSources[pr.TargetBranch], nil
		}
	}
	return false, nil
}

func (m *Manager) ApplySCMObservation(ctx context.Context, id domain.SessionID, o ports.SCMObservation) error {
	if !o.Fetched {
		return nil
	}
	if err := m.ApplyPRObservation(ctx, id, scmToPRObservation(o)); err != nil {
		return err
	}
	ready, err := m.scmObservationReadyToMerge(ctx, o)
	if err != nil {
		return err
	}
	intent, err := m.notificationIntentForCurrentSCM(ctx, id, o, ready)
	if err != nil {
		return err
	}
	m.emitNotification(ctx, intent)
	m.resolveNotifications(ctx, readyToMergeResolutions(id, o, ready, m.clock())...)
	return nil
}

func readyToMergeResolutions(id domain.SessionID, o ports.SCMObservation, ready bool, now time.Time) []ports.NotificationResolution {
	if ready {
		return nil
	}
	return []ports.NotificationResolution{{
		Type:       domain.NotificationReadyToMerge,
		SessionID:  id,
		PRURL:      firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL),
		ResolvedAt: timeOr(o.ObservedAt, now),
	}}
}

func (m *Manager) notificationIntentForCurrentSCM(ctx context.Context, id domain.SessionID, o ports.SCMObservation, ready bool) (*ports.NotificationIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return m.notificationIntentForSCM(rec, o, ready), nil
}

func (m *Manager) notificationIntentForSCM(rec domain.SessionRecord, o ports.SCMObservation, ready bool) *ports.NotificationIntent {
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	base := ports.NotificationIntent{
		SessionID:          rec.ID,
		ProjectID:          rec.ProjectID,
		PRURL:              prURL,
		CreatedAt:          timeOr(o.ObservedAt, m.clock()),
		SessionDisplayName: rec.DisplayName,
		PRNumber:           o.PR.Number,
		PRTitle:            o.PR.Title,
		PRSourceBranch:     o.PR.SourceBranch,
		PRTargetBranch:     o.PR.TargetBranch,
		Provider:           o.Provider,
		Repo:               o.Repo,
	}
	if o.PR.Merged {
		base.Type = domain.NotificationPRMerged
		return &base
	}
	if o.PR.Closed {
		base.Type = domain.NotificationPRClosedUnmerged
		return &base
	}
	if rec.IsTerminated || rec.Activity.State.NeedsInput() || !ready {
		return nil
	}
	base.Type = domain.NotificationReadyToMerge
	return &base
}

func (m *Manager) scmObservationReadyToMerge(ctx context.Context, o ports.SCMObservation) (bool, error) {
	ready := domain.MergeReadiness{
		Draft:              o.PR.Draft,
		Merged:             o.PR.Merged,
		Closed:             o.PR.Closed,
		CI:                 domain.CIState(o.CI.Summary),
		Review:             domain.ReviewDecision(o.Review.Decision),
		Mergeability:       domain.Mergeability(o.Mergeability.State),
		UnresolvedComments: hasUnresolvedSCMComments(o.Review.Threads),
	}.ReadyToMerge()
	if !ready {
		return false, nil
	}
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	comments, err := m.store.ListPRComments(ctx, prURL)
	if err != nil {
		return false, fmt.Errorf("list persisted comments for %s: %w", prURL, err)
	}
	for _, comment := range comments {
		if domain.IsActionableReviewComment(comment.Resolved, comment.IsBot, comment.File, comment.Line) {
			return false, nil
		}
	}
	return true, nil
}

func hasUnresolvedSCMComments(threads []ports.SCMReviewThreadObservation) bool {
	for _, th := range threads {
		for _, c := range th.Comments {
			if domain.IsActionableReviewComment(th.Resolved, c.IsBot, th.Path, th.Line) {
				return true
			}
		}
	}
	return false
}

func scmToPRObservation(o ports.SCMObservation) ports.PRObservation {
	pr := ports.PRObservation{
		Fetched:      o.Fetched,
		URL:          firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL),
		Number:       o.PR.Number,
		Title:        o.PR.Title,
		SourceBranch: o.PR.SourceBranch,
		TargetBranch: o.PR.TargetBranch,
		Draft:        o.PR.Draft,
		Merged:       o.PR.Merged,
		Closed:       o.PR.Closed,
		CI:           domain.CIState(o.CI.Summary),
		Review:       domain.ReviewDecision(o.Review.Decision),
		Mergeability: domain.Mergeability(o.Mergeability.State),
	}
	if pr.CI == "" {
		pr.CI = domain.CIUnknown
	}
	if pr.Review == "" {
		pr.Review = domain.ReviewNone
	}
	if pr.Mergeability == "" {
		pr.Mergeability = domain.MergeUnknown
	}
	checkCommit := firstSCMNonEmpty(o.CI.HeadSHA, o.PR.HeadSHA)
	for _, ch := range o.CI.FailedChecks {
		status := domain.PRCheckStatus(ch.Status)
		if status == "" {
			status = domain.PRCheckFailed
		}
		logTail := ch.LogTail
		if logTail == "" {
			logTail = o.CI.FailureLogTail
		}
		pr.Checks = append(pr.Checks, ports.PRCheckObservation{
			Name:       ch.Name,
			CommitHash: checkCommit,
			Status:     status,
			URL:        ch.URL,
			LogTail:    logTail,
		})
	}
	return pr
}

func withoutPRAuthorReplies(comments []domain.PullRequestComment, prAuthor string) []domain.PullRequestComment {
	author := normalizeCommentAuthor(prAuthor)
	if author == "" {
		return comments
	}
	seenThread := make(map[string]bool, len(comments))
	out := make([]domain.PullRequestComment, 0, len(comments))
	for _, comment := range comments {
		thread := strings.TrimSpace(comment.ThreadID)
		reply := thread != "" && seenThread[thread]
		if thread != "" {
			seenThread[thread] = true
		}
		if reply && normalizeCommentAuthor(comment.Author) == author {
			continue
		}
		out = append(out, comment)
	}
	return out
}

func normalizeCommentAuthor(login string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(login), "@"))
}

func prCommentObservations(comments []domain.PullRequestComment) []ports.PRCommentObservation {
	out := make([]ports.PRCommentObservation, 0, len(comments))
	for _, comment := range comments {
		if !domain.IsActionableReviewComment(comment.Resolved, comment.IsBot, comment.File, comment.Line) {
			continue
		}
		out = append(out, ports.PRCommentObservation{
			ID:               comment.ID,
			ThreadID:         comment.ThreadID,
			ReviewID:         comment.ReviewID,
			Author:           comment.Author,
			File:             comment.File,
			Line:             comment.Line,
			Body:             comment.Body,
			URL:              comment.URL,
			Resolved:         comment.Resolved,
			AutoInjectReview: comment.AutoInjectReview,
		})
	}
	return out
}

func (m *Manager) ApplyTrackerFacts(ctx context.Context, id domain.SessionID, o ports.TrackerObservation) error {
	if !o.Fetched {
		return nil
	}
	if isTerminalTrackerState(o.Issue.State) {
		if m.sessionMutationInProgress(id) {
			return nil
		}
		before, live := m.liveBeforeEnd(ctx, id)
		err := m.MarkTerminated(ctx, id)
		m.reportEndIfTerminated(ctx, before, live, err, deliverytelemetry.EndIssueClosed)
		return err
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if cannotNudge(rec) {
		return nil
	}
	if o.Changed.Assignee {
		slog.Default().Info("lifecycle: tracker issue assignee changed",
			"session", id, "issue", o.Issue.URL, "assignee", o.Issue.Assignee)
	}
	if o.Changed.Comments {
		bodies, ids := newBotCommentContent(o.Comments)
		if len(ids) > 0 {
			msg := "A bot left a new comment on your tracker issue. Address it and update the session."
			if joined := strings.Join(bodies, "\n\n"); strings.TrimSpace(joined) != "" {
				msg += "\n\n" + joined
			}
			_, err := m.sendOnce(ctx, id, "", "tracker-bot:"+o.Issue.URL, strings.Join(ids, ","), msg, 0, false)
			return err
		}
	}
	return nil
}

func cannotNudge(rec domain.SessionRecord) bool {
	return rec.IsTerminated || rec.Activity.State.NeedsInput() || rec.Activity.State == domain.ActivityExited
}

func isTerminalTrackerState(state domain.NormalizedIssueState) bool {
	return state == domain.IssueDone || state == domain.IssueCancelled
}

func newBotCommentContent(comments []ports.TrackerCommentObservation) ([]string, []string) {
	bodies := make([]string, 0, len(comments))
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		if !c.IsBot {
			continue
		}
		if c.ID == "" || strings.TrimSpace(c.Body) == "" {
			continue
		}
		bodies = append(bodies, c.Body)
		ids = append(ids, c.ID)
	}
	return bodies, ids
}

func firstSCMNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func prIdentity(o ports.PRObservation) string {
	if o.Number <= 0 {
		return "your PR"
	}
	id := fmt.Sprintf("PR #%d", o.Number)
	if o.Title != "" {
		id += fmt.Sprintf(" %q", domain.SanitizeControlChars(o.Title))
	}
	if o.SourceBranch != "" && o.TargetBranch != "" {
		id += fmt.Sprintf(" (%s → %s)", domain.SanitizeControlChars(o.SourceBranch), domain.SanitizeControlChars(o.TargetBranch))
	}
	return id
}

func hasUnresolvedComments(comments []ports.PRCommentObservation) bool {
	for _, c := range comments {
		if !c.Resolved {
			return true
		}
	}
	return false
}

func failedPRChecks(checks []ports.PRCheckObservation) []ports.PRCheckObservation {
	failed := make([]ports.PRCheckObservation, 0, len(checks))
	for _, ch := range checks {
		if ch.Status == domain.PRCheckFailed {
			failed = append(failed, ch)
		}
	}
	return failed
}

func ciFailureSignature(checks []ports.PRCheckObservation) string {
	parts := make([]string, 0, len(checks))
	for _, ch := range checks {
		parts = append(parts, strings.Join([]string{ch.Name, ch.CommitHash, string(ch.Status), ch.URL, ch.LogTail}, "\x00"))
	}
	return strings.Join(parts, "\x01")
}

func formatCIFailureMessage(checks []ports.PRCheckObservation) string {
	var msg strings.Builder
	msg.WriteString("CI is failing on your PR.\n")
	for _, ch := range checks {
		name := domain.SanitizeControlChars(ch.Name)
		if strings.TrimSpace(name) == "" {
			name = "unnamed check"
		}
		status := domain.SanitizeControlChars(string(ch.Status))
		if strings.TrimSpace(status) == "" {
			status = "failed"
		}
		fmt.Fprintf(&msg, "\nFailed: %s (%s)", name, status)
		if ch.URL != "" {
			fmt.Fprintf(&msg, "\nFailure URL: %s", domain.SanitizeControlChars(ch.URL))
		}
		if ch.LogTail != "" {
			tail := domain.SanitizeControlChars(ch.LogTail)
			fence := markdownCodeFence(tail)
			lineCount := len(strings.Split(tail, "\n"))
			lineLabel := "lines"
			if lineCount == 1 {
				lineLabel = "line"
			}
			fmt.Fprintf(&msg, "\n\nLog tail (last %d %s):\n%s\n%s\n%s", lineCount, lineLabel, fence, tail, fence)
		}
		msg.WriteString("\n")
	}
	msg.WriteString("\nUse the included log tail and failure URL first; fetch full CI logs only if you need additional context. Fix the issues and push again.")
	return msg.String()
}

func unresolvedReviewComments(comments []ports.PRCommentObservation) []ports.PRCommentObservation {
	unresolved := make([]ports.PRCommentObservation, 0, len(comments))
	for _, c := range comments {
		if c.Resolved {
			continue
		}
		unresolved = append(unresolved, c)
	}
	return unresolved
}

func reviewCommentsSignature(comments []ports.PRCommentObservation) string {
	parts := make([]string, 0, len(comments))
	for _, c := range comments {
		id := strings.TrimSpace(c.ID)
		threadID := strings.TrimSpace(c.ThreadID)
		if id == "" && threadID == "" {
			continue
		}
		parts = append(parts, threadID+"\x00"+id)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

func formatReviewChangesRequestedMessage(review domain.PullRequestReview) string {
	var msg strings.Builder
	author := domain.SanitizeControlChars(review.Author)
	if strings.TrimSpace(author) == "" {
		author = "unknown reviewer"
	}
	fmt.Fprintf(&msg, "A changes-requested review from @%s is on your PR.", author)
	if body := domain.SanitizeControlChars(review.Body); strings.TrimSpace(body) != "" {
		fmt.Fprintf(&msg, "\n\nReview body:\n%s", body)
	}
	if review.URL != "" {
		fmt.Fprintf(&msg, "\n\nReview URL: %s", domain.SanitizeControlChars(review.URL))
	}
	if review.ID != "" {
		fmt.Fprintf(&msg, "\nReview ID: %s", domain.SanitizeControlChars(review.ID))
	}
	msg.WriteString("\n\nAddress the requested changes and push. You should not need to re-fetch the review unless you need additional context beyond what Orchestrator has provided here.")
	return msg.String()
}

func formatReviewCommentsMessage(comments []ports.PRCommentObservation) string {
	if len(comments) == 0 {
		return "A reviewer left feedback on your PR. Address it and push. Fetch the review details only if you need additional context beyond what Orchestrator has provided here."
	}
	var msg strings.Builder
	fmt.Fprintf(&msg, "The following %d unresolved review comment(s) are on your PR as of just now. You should not need to re-fetch this data unless you need additional context.\n", len(comments))
	for i, c := range comments {
		location := "(general)"
		if c.File != "" {
			location = domain.SanitizeControlChars(c.File)
			if c.Line > 0 {
				location = fmt.Sprintf("%s:%d", location, c.Line)
			}
		}
		author := domain.SanitizeControlChars(c.Author)
		if strings.TrimSpace(author) == "" {
			author = "unknown reviewer"
		}
		body := domain.SanitizeControlChars(c.Body)
		fmt.Fprintf(&msg, "\n%d. %s (@%s):\n%s", i+1, location, author, body)
		if c.URL != "" {
			fmt.Fprintf(&msg, "\n   %s", domain.SanitizeControlChars(c.URL))
		}
		if c.ThreadID != "" {
			fmt.Fprintf(&msg, "\n   Thread ID: %s", domain.SanitizeControlChars(c.ThreadID))
		}
		msg.WriteString("\n")
	}
	msg.WriteString("\nAddress each comment and push fixes. Use the thread ID to resolve each thread directly after pushing when available. You should not need to re-fetch review data unless you need additional context beyond what is provided here.")
	return msg.String()
}

func markdownCodeFence(s string) string {
	maxRun := 0
	run := 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
			continue
		}
		run = 0
	}
	if maxRun < 3 {
		return "```"
	}
	return strings.Repeat("`", maxRun+1)
}

type sendOnceOutcome int

const (
	sendOnceAccounted sendOnceOutcome = iota
	sendOnceSuppressed
)

func (m *Manager) sendOnce(ctx context.Context, id domain.SessionID, prURL, key, sig, msg string, maxAttempts int, urgent bool) (sendOnceOutcome, error) {
	if m.guard == nil {
		return sendOnceAccounted, nil
	}
	m.react.mu.Lock()
	defer m.react.mu.Unlock()

	if prURL != "" && !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return sendOnceAccounted, err
		}
		m.react.loaded[prURL] = true
	}

	if m.react.seen[key] == sig {
		return sendOnceAccounted, nil
	}
	attempts := m.react.attempts[key]
	if maxAttempts > 0 && attempts >= maxAttempts {
		return sendOnceAccounted, nil
	}
	nudge := m.guard.Nudge
	if urgent {
		nudge = func(ctx context.Context, id domain.SessionID, msg string) (sessionguard.Outcome, error) {
			return m.guard.NudgeUrgent(ctx, id, msg, m.urgentNudgeWaitingInputSafe)
		}
	}
	outcome, err := nudge(ctx, id, msg)
	if err != nil {
		if outcome != sessionguard.Sent {
			return sendOnceSuppressed, err
		}
		return sendOnceAccounted, err
	}
	if outcome != sessionguard.Sent {
		return sendOnceSuppressed, nil
	}
	m.react.seen[key] = sig
	m.react.attempts[key] = attempts + 1
	if prURL != "" {
		if err := m.persistPRSignaturesLocked(ctx, prURL); err != nil {
			return sendOnceAccounted, err
		}
	}
	return sendOnceAccounted, nil
}

func (m *Manager) loadPRSignaturesLocked(ctx context.Context, prURL string) error {
	raw, err := m.store.GetPRLastNudgeSignature(ctx, prURL)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	var p reactionPayload
	_ = json.Unmarshal([]byte(raw), &p)
	for k, v := range p.Seen {
		if _, ok := m.react.seen[k]; !ok {
			m.react.seen[k] = v
		}
	}
	for k, v := range p.Attempts {
		if cur, ok := m.react.attempts[k]; !ok || v > cur {
			m.react.attempts[k] = v
		}
	}
	return nil
}

func (m *Manager) persistPRSignaturesLocked(ctx context.Context, prURL string) error {
	payload := reactionPayload{Seen: map[string]string{}, Attempts: map[string]int{}}
	for k, v := range m.react.seen {
		if reactionKeyTargetsPR(k, prURL) {
			payload.Seen[k] = v
		}
	}
	for k, v := range m.react.attempts {
		if reactionKeyTargetsPR(k, prURL) {
			payload.Attempts[k] = v
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.store.UpdatePRLastNudgeSignature(ctx, prURL, string(raw))
}

func reactionKeyTargetsPR(key, prURL string) bool {
	if prURL == "" {
		return false
	}
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return false
	}
	rest := parts[1]
	return rest == prURL || strings.HasPrefix(rest, prURL+":")
}
