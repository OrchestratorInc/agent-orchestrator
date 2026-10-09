package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

type pullRequestFailingCheckResponse struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

type mergePullRequestRequest struct {
	PRURL           string `json:"prUrl"`
	ExpectedHeadSHA string `json:"expectedHeadSha"`
}

func pullRequestSnapshotReadyForMerge(snapshot domain.PullRequestSnapshot, expectedHeadSHA string) bool {
	state := snapshot.Observation
	if state.State != contract.PRStateOpen || state.Draft || state.HeadSHA != expectedHeadSHA ||
		state.CIState != contract.CIPassing || state.Mergeability != contract.MergeMergeable ||
		(state.ReviewState != contract.ReviewApproved && state.ReviewState != contract.ReviewNone) || snapshot.ReviewsPartial {
		return false
	}
	for _, comment := range snapshot.Comments {
		if !comment.IsBot && !comment.Resolved && !comment.Outdated {
			return false
		}
	}
	return true
}

func (s *Server) mergeSessionPullRequest(w http.ResponseWriter, r *http.Request) {
	orgID, sessionID := chi.URLParam(r, "orgId"), chi.URLParam(r, "sessionId")
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil || number <= 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "A valid organization, session and PR number are required.")
		return
	}
	var input mergePullRequestRequest
	if err := decodeJSON(w, r, &input); err != nil || strings.TrimSpace(input.ExpectedHeadSHA) == "" || strings.TrimSpace(input.PRURL) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "A PR URL and expected head SHA are required.")
		return
	}
	pr, err := s.store.PullRequestForMerge(r.Context(), principalFrom(r), orgID, sessionID, number, input.PRURL)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if pr.Provider != "github" || pr.State != "open" || pr.Draft || pr.HeadSHA != input.ExpectedHeadSHA {
		writeError(w, r, http.StatusConflict, "pr_not_mergeable", "The pull request has changed. Refresh its status before merging.")
		return
	}
	snapshot, err := s.store.PullRequestSnapshot(r.Context(), orgID, pr.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if pr.CIState != "passing" || pr.Mergeability != "mergeable" ||
		(pr.ReviewState != "approved" && pr.ReviewState != "none") ||
		toPullRequestSummaryResponse(pr, snapshot).Review.HasUnresolvedHumanComments {
		writeError(w, r, http.StatusConflict, "pr_not_mergeable", "The pull request is not ready to merge.")
		return
	}
	owner, repo, ok := strings.Cut(pr.Repository, "/")
	if !ok || owner == "" || repo == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_repository", "The pull request repository is invalid.")
		return
	}
	// Credential precedence: prefer the GitHub App when the control plane has one
	// configured. The App is the installation-scoped credential that opened the PR
	// and is always current; a caller's stored PAT is a cached snapshot that can be
	// revoked while still recorded as validation_state=valid — preferring it let a
	// dead PAT shadow a healthy App and fail every merge with a 401 ("GitHub could
	// not refresh this pull request"). The PAT is used only when there is no App
	// (an App-less deployment) or as a fallback when the App path cannot serve the
	// merge (e.g. the App is not installed on that repository). This mirrors the
	// worker checkout/push/pull-request credential precedence. The webhook remains
	// responsible for recording the merged state.
	merged := false
	appTried := false
	if s.github != nil {
		appTried = true
		fresh, fetchErr := s.github.FetchPullRequestSnapshot(r.Context(), domain.PullRequestRef{ID: pr.ID, OrgID: orgID, Provider: pr.Provider, Repository: pr.Repository, Number: number})
		if fetchErr != nil {
			// Do not fail outright: fall through to a PAT fallback if one exists.
			s.logger.Warn("refresh pull request via GitHub App before merge; will try PAT fallback", "error", fetchErr, "request_id", requestID(r))
		} else {
			if !pullRequestSnapshotReadyForMerge(fresh, input.ExpectedHeadSHA) {
				writeError(w, r, http.StatusConflict, "pr_not_mergeable", "The pull request has changed. Refresh its status before merging.")
				return
			}
			err = s.github.MergePullRequest(r.Context(), orgID, pr.Repository, number, input.ExpectedHeadSHA)
			merged = true
		}
	}
	if !merged && s.patWrites != nil && s.secretCipher != nil {
		if credentialStore, ok := s.store.(userProviderConnectionStore); ok {
			principal := principalFrom(r)
			encrypted, nonce, credentialErr := credentialStore.UserProviderConnectionSecret(r.Context(), principal, githubPATProvider, defaultAgentConnectionLabel)
			if credentialErr == nil {
				secret, decryptErr := s.secretCipher.Decrypt(encrypted, nonce, providerSecretAssociatedData("user:"+principal.UserID, githubPATProvider))
				if decryptErr != nil {
					writeError(w, r, http.StatusInternalServerError, "internal_error", "The GitHub token could not be read.")
					return
				}
				defer clear(secret)
				fresh, fetchErr := s.patWrites.FetchPullRequestSnapshot(r.Context(), string(secret), owner, repo, number)
				if fetchErr != nil {
					s.logger.Error("refresh pull request before merge", "error", fetchErr, "request_id", requestID(r))
					writeError(w, r, http.StatusBadGateway, "github_unavailable", "GitHub could not refresh this pull request.")
					return
				}
				if !pullRequestSnapshotReadyForMerge(fresh, input.ExpectedHeadSHA) {
					writeError(w, r, http.StatusConflict, "pr_not_mergeable", "The pull request has changed. Refresh its status before merging.")
					return
				}
				err = s.patWrites.MergePullRequest(r.Context(), string(secret), owner, repo, number, input.ExpectedHeadSHA)
				merged = true
			} else if !errors.Is(credentialErr, postgres.ErrNotFound) {
				s.writeStoreError(w, r, credentialErr)
				return
			}
		}
	}
	if !merged {
		if appTried {
			// An App was configured but its refresh failed and no PAT could serve
			// the merge as a fallback — surface the refresh failure.
			writeError(w, r, http.StatusBadGateway, "github_unavailable", "GitHub could not refresh this pull request.")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "github_unavailable", "GitHub merge is not configured for this account.")
		return
	}
	if err != nil {
		var githubErr *githubapp.HTTPError
		if errors.As(err, &githubErr) && (githubErr.StatusCode == http.StatusConflict || githubErr.StatusCode == http.StatusMethodNotAllowed || githubErr.StatusCode == http.StatusUnprocessableEntity) {
			writeError(w, r, http.StatusConflict, "pr_not_mergeable", "GitHub could not merge this pull request. Refresh its status and try again.")
			return
		}
		s.logger.Error("merge GitHub pull request", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "github_unavailable", "GitHub could not merge this pull request.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "merge_accepted"})
}

type pullRequestCISummaryResponse struct {
	State         string                            `json:"state"`
	FailingChecks []pullRequestFailingCheckResponse `json:"failingChecks"`
}

type pullRequestReviewCommentLinkResponse struct {
	URL              string `json:"url"`
	ReviewID         string `json:"reviewId,omitempty"`
	File             string `json:"file,omitempty"`
	Line             int    `json:"line,omitempty"`
	Body             string `json:"body,omitempty"`
	AutoInjectReview bool   `json:"autoInjectReview"`
}

type pullRequestUnresolvedReviewerResponse struct {
	ReviewerID string                                 `json:"reviewerId"`
	Count      int                                    `json:"count"`
	Links      []pullRequestReviewCommentLinkResponse `json:"links"`
	ReviewURL  string                                 `json:"reviewUrl,omitempty"`
	IsBot      bool                                   `json:"isBot,omitempty"`
}

type pullRequestSubmittedReviewResponse struct {
	ReviewerID       string     `json:"reviewerId"`
	Verdict          string     `json:"verdict"`
	Body             string     `json:"body,omitempty"`
	ReviewURL        string     `json:"reviewUrl,omitempty"`
	SubmittedAt      *time.Time `json:"submittedAt,omitempty"`
	IsBot            bool       `json:"isBot,omitempty"`
	AutoInjectReview bool       `json:"autoInjectReview"`
}

type pullRequestReviewSummaryResponse struct {
	Decision                   string                                  `json:"decision"`
	HasUnresolvedHumanComments bool                                    `json:"hasUnresolvedHumanComments"`
	UnresolvedBy               []pullRequestUnresolvedReviewerResponse `json:"unresolvedBy"`
	ResolvedBy                 []pullRequestUnresolvedReviewerResponse `json:"resolvedBy"`
	Reviews                    []pullRequestSubmittedReviewResponse    `json:"reviews"`
}

type pullRequestConflictFileResponse struct {
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

type pullRequestMergeabilitySummaryResponse struct {
	State          string                            `json:"state"`
	Reasons        []string                          `json:"reasons"`
	PullRequestURL string                            `json:"pullRequestUrl"`
	ConflictFiles  []pullRequestConflictFileResponse `json:"conflictFiles"`
}

type pullRequestSummaryResponse struct {
	URL              string                                 `json:"url"`
	HTMLURL          string                                 `json:"htmlUrl,omitempty"`
	Number           int                                    `json:"number"`
	Title            string                                 `json:"title"`
	State            string                                 `json:"state"`
	Provider         string                                 `json:"provider"`
	Repository       string                                 `json:"repository"`
	Author           string                                 `json:"author"`
	AuthorAvatarURL  string                                 `json:"authorAvatarUrl,omitempty"`
	SourceBranch     string                                 `json:"sourceBranch"`
	TargetBranch     string                                 `json:"targetBranch"`
	HeadSHA          string                                 `json:"headSha"`
	Additions        int                                    `json:"additions"`
	Deletions        int                                    `json:"deletions"`
	ChangedFiles     int                                    `json:"changedFiles"`
	CI               pullRequestCISummaryResponse           `json:"ci"`
	Review           pullRequestReviewSummaryResponse       `json:"review"`
	Mergeability     pullRequestMergeabilitySummaryResponse `json:"mergeability"`
	StateChangedAt   *time.Time                             `json:"stateChangedAt,omitempty"`
	CreatedAt        *time.Time                             `json:"createdAt,omitempty"`
	UpdatedAt        time.Time                              `json:"updatedAt"`
	ObservedAt       time.Time                              `json:"observedAt"`
	CIObservedAt     time.Time                              `json:"ciObservedAt"`
	ReviewObservedAt time.Time                              `json:"reviewObservedAt"`
}

func toPullRequestSummaryResponse(pr domain.PullRequest, snapshot domain.PullRequestSnapshot) pullRequestSummaryResponse {
	createdAt := pr.CreatedAt
	review := pullRequestReviewResponse(pr, snapshot)
	reasons := []string{}
	if pr.CIState == contract.CIUnknown && !pr.ObservedAt.IsZero() {
		reasons = append(reasons, "github_checks_unavailable")
	}
	if review.HasUnresolvedHumanComments {
		reasons = append(reasons, "unresolved_comments")
	}
	return pullRequestSummaryResponse{
		URL:             pr.URL,
		HTMLURL:         pr.URL,
		Number:          pr.Number,
		Title:           pr.Title,
		State:           string(pr.State),
		Provider:        pr.Provider,
		Repository:      pr.Repository,
		Author:          pr.Author,
		AuthorAvatarURL: pr.AuthorAvatarURL,
		SourceBranch:    pr.SourceBranch,
		TargetBranch:    pr.TargetBranch,
		HeadSHA:         pr.HeadSHA,
		Additions:       pr.Additions,
		Deletions:       pr.Deletions,
		ChangedFiles:    pr.ChangedFiles,
		CI: pullRequestCISummaryResponse{
			State:         string(pr.CIState),
			FailingChecks: pullRequestFailingChecks(pr.Checks),
		},
		Review: review,
		Mergeability: pullRequestMergeabilitySummaryResponse{
			State:          string(pr.Mergeability),
			Reasons:        reasons,
			PullRequestURL: pr.URL,
			ConflictFiles:  []pullRequestConflictFileResponse{},
		},
		CreatedAt:        &createdAt,
		UpdatedAt:        pr.UpdatedAt,
		ObservedAt:       pr.ObservedAt,
		CIObservedAt:     pr.ObservedAt,
		ReviewObservedAt: pr.ObservedAt,
	}
}

func pullRequestReviewResponse(pr domain.PullRequest, snapshot domain.PullRequestSnapshot) pullRequestReviewSummaryResponse {
	out := pullRequestReviewSummaryResponse{Decision: string(pr.ReviewState), UnresolvedBy: []pullRequestUnresolvedReviewerResponse{}, ResolvedBy: []pullRequestUnresolvedReviewerResponse{}, Reviews: []pullRequestSubmittedReviewResponse{}}
	type group struct {
		count int
		links []pullRequestReviewCommentLinkResponse
		bot   bool
	}
	unresolved := map[string]*group{}
	resolved := map[string]*group{}
	for _, comment := range snapshot.Comments {
		if comment.IsBot {
			continue
		}
		reviewer := comment.Author
		if reviewer == "" {
			reviewer = "unknown"
		}
		target := unresolved
		if comment.Resolved || comment.Outdated {
			target = resolved
		}
		entry := target[reviewer]
		if entry == nil {
			entry = &group{}
			target[reviewer] = entry
		}
		entry.count++
		entry.links = append(entry.links, pullRequestReviewCommentLinkResponse{URL: comment.URL, ReviewID: comment.ReviewProviderID, File: comment.Path, Line: comment.Line, Body: comment.Body, AutoInjectReview: comment.AutoInjectReview})
	}
	appendGroups := func(source map[string]*group) []pullRequestUnresolvedReviewerResponse {
		keys := make([]string, 0, len(source))
		for key := range source {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]pullRequestUnresolvedReviewerResponse, 0, len(keys))
		for _, key := range keys {
			value := source[key]
			result = append(result, pullRequestUnresolvedReviewerResponse{ReviewerID: key, Count: value.count, Links: value.links, IsBot: value.bot})
		}
		return result
	}
	out.UnresolvedBy = appendGroups(unresolved)
	out.ResolvedBy = appendGroups(resolved)
	out.HasUnresolvedHumanComments = len(out.UnresolvedBy) > 0
	latest := map[string]domain.PullRequestReview{}
	for _, review := range snapshot.Reviews {
		reviewer := review.Author
		if reviewer == "" {
			reviewer = "unknown"
		}
		current, ok := latest[reviewer]
		if !ok || reviewTimeAfter(review, current) {
			latest[reviewer] = review
		}
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		review := latest[key]
		out.Reviews = append(out.Reviews, pullRequestSubmittedReviewResponse{ReviewerID: key, Verdict: string(review.State), Body: review.Body, ReviewURL: review.URL, SubmittedAt: review.SubmittedAt, IsBot: review.IsBot, AutoInjectReview: review.AutoInjectReview})
	}
	return out
}

func reviewTimeAfter(left, right domain.PullRequestReview) bool {
	if left.SubmittedAt == nil {
		return false
	}
	if right.SubmittedAt == nil {
		return true
	}
	return left.SubmittedAt.After(*right.SubmittedAt)
}

func pullRequestFailingChecks(snapshot json.RawMessage) []pullRequestFailingCheckResponse {
	var checks []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
		URL        string `json:"url"`
	}
	if len(snapshot) == 0 || json.Unmarshal(snapshot, &checks) != nil {
		return []pullRequestFailingCheckResponse{}
	}
	result := make([]pullRequestFailingCheckResponse, 0, len(checks))
	for _, check := range checks {
		switch check.Conclusion {
		case "failure", "timed_out", "action_required", "startup_failure", "cancelled":
			status := "failed"
			if check.Conclusion == "cancelled" {
				status = "cancelled"
			}
			result = append(result, pullRequestFailingCheckResponse{
				Name: check.Name, Status: status, Conclusion: check.Conclusion, URL: firstNonEmptyString(check.HTMLURL, check.URL),
			})
		}
	}
	return result
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Server) listSessionPullRequests(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	pullRequests, err := s.store.ListPullRequestsBySession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	items := make([]pullRequestSummaryResponse, 0, len(pullRequests))
	for _, pr := range pullRequests {
		snapshot, err := s.store.PullRequestSnapshot(r.Context(), orgID, pr.ID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		item := toPullRequestSummaryResponse(pr, snapshot)
		if s.github != nil && pr.Provider == "github" && (pr.State == contract.PRStateOpen || pr.State == contract.PRStateDraft) {
			_, _, err := s.store.GitHubInstallationForRepository(r.Context(), orgID, pr.Repository)
			if errors.Is(err, postgres.ErrNotFound) {
				// An uninstalled App leaves the last observed PR facts in storage.
				// Surface the revoked grant at read time so stale facts cannot look merge-ready.
				item.Mergeability.State = string(contract.MergeUnknown)
				item.Mergeability.Reasons = []string{"github_access_lost"}
			} else if err != nil {
				s.writeStoreError(w, r, err)
				return
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "pullRequests": items})
}

func (s *Server) sendReviewToWorker(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	reviewRunID := chi.URLParam(r, "reviewRunId")
	if requireUUID(orgID, "orgId") != nil ||
		requireUUID(sessionID, "sessionId") != nil ||
		requireUUID(reviewRunID, "reviewRunId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId, sessionId, and reviewRunId must be UUIDs.")
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	runs, err := s.store.ListReviewRunsBySession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	var selected *domain.ReviewRunPullRequest
	for index := range runs {
		if runs[index].ID == reviewRunID {
			selected = &runs[index]
			break
		}
	}
	if selected == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Review run not found.")
		return
	}
	if selected.Status != contract.AOReviewRunDelivered && selected.Status != contract.AOReviewRunComplete {
		writeError(w, r, http.StatusConflict, "review_not_ready", "The review is not ready to send to the worker.")
		return
	}
	body := strings.TrimSpace(selected.Body)
	if body == "" {
		writeError(w, r, http.StatusConflict, "review_not_ready", "The review has no feedback to send to the worker.")
		return
	}
	harness := strings.TrimSpace(selected.Harness)
	if harness == "" {
		harness = "reviewer"
	}
	message := fmt.Sprintf(
		"An AO agent review from %s has feedback for your pull request. Address the actionable items, run relevant tests, commit the fixes, and push the branch.\n\nReview summary:\n%s",
		harness,
		body,
	)
	reviewURL := strings.TrimSpace(selected.PullRequestURL)
	if providerReviewID := strings.TrimSpace(selected.ProviderReviewID); reviewURL != "" && providerReviewID != "" {
		reviewURL += "#pullrequestreview-" + providerReviewID
	}
	if reviewURL != "" {
		message += "\n\nReview URL: " + reviewURL
	}
	event, err := s.store.SendMessage(r.Context(), principalFrom(r), orgID, sessionID, key, message, domain.ChatTurnSettings{})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"event": toClientEventResponse(event)})
}

type aoReviewRunResponse struct {
	ID                 string     `json:"id"`
	ReviewID           string     `json:"reviewId"`
	SessionID          string     `json:"sessionId"`
	BatchID            string     `json:"batchId"`
	Harness            string     `json:"harness"`
	TriggerSource      string     `json:"triggerSource"`
	PullRequestURL     string     `json:"pullRequestUrl"`
	TargetSHA          string     `json:"targetSha"`
	Status             string     `json:"status"`
	Verdict            string     `json:"verdict"`
	Body               string     `json:"body"`
	ProviderReviewID   string     `json:"providerReviewId"`
	ReviewerTerminalID string     `json:"reviewerTerminalId,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	DeliveredAt        *time.Time `json:"deliveredAt,omitempty"`
	AutoInjectReview   bool       `json:"autoInjectReview"`
}

func toAOReviewRunResponse(run domain.ReviewRunPullRequest, harness string) aoReviewRunResponse {
	if run.Harness != "" {
		harness = run.Harness
	}
	return aoReviewRunResponse{
		ID:        run.ID,
		ReviewID:  run.ID,
		SessionID: run.ReviewSessionID,
		// Cloud runs are one-pass batches. Keep the stable run ID here rather
		// than inventing a second grouping record just to satisfy the shared
		// inspector's history model.
		BatchID:            run.ID,
		Harness:            harness,
		TriggerSource:      run.TriggerSource,
		PullRequestURL:     run.PullRequestURL,
		TargetSHA:          run.TargetSHA,
		Status:             string(run.Status),
		Verdict:            string(run.Verdict),
		Body:               run.Body,
		ProviderReviewID:   run.ProviderReviewID,
		ReviewerTerminalID: run.ReviewTerminalID,
		CreatedAt:          run.CreatedAt,
		DeliveredAt:        run.DeliveredAt,
	}
}

type aoPullRequestReviewStateResponse struct {
	PullRequestURL    string               `json:"pullRequestUrl"`
	PullRequestNumber int                  `json:"pullRequestNumber"`
	Title             string               `json:"title"`
	TargetSHA         string               `json:"targetSha"`
	Status            string               `json:"status"`
	LatestRun         *aoReviewRunResponse `json:"latestRun,omitempty"`
	PreviousRun       *aoReviewRunResponse `json:"previousRun,omitempty"`
}

func (s *Server) sessionReviewPayload(r *http.Request, orgID, sessionID string) (map[string]any, error) {
	session, err := s.store.GetSession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		return nil, err
	}
	prs, err := s.store.ListPullRequestsBySession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		return nil, err
	}
	runs, err := s.store.ListReviewRunsBySession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		return nil, err
	}
	reviewerHarness := session.ReviewerHarness
	if reviewerHarness == "" {
		reviewerHarness = session.Harness
	}
	availableReviewerHarnesses := []string{}
	if preferences, ok := s.store.(sessionPreferencesStore); ok {
		availableReviewerHarnesses, err = preferences.AvailableSessionReviewerHarnesses(r.Context(), principalFrom(r), orgID, sessionID)
		if err != nil {
			return nil, err
		}
	}
	allRuns := make([]aoReviewRunResponse, 0, len(runs))
	runsByPR := make(map[string][]domain.ReviewRunPullRequest, len(prs))
	for _, run := range runs {
		allRuns = append(allRuns, toAOReviewRunResponse(run, reviewerHarness))
		runsByPR[run.PullRequestID] = append(runsByPR[run.PullRequestID], run)
	}
	reviews := make([]aoPullRequestReviewStateResponse, 0, len(prs))
	reviewerHandleID := ""
	for _, pr := range prs {
		state := reviewStateForPullRequest(pr, runsByPR[pr.ID])
		response := aoPullRequestReviewStateResponse{
			PullRequestURL: pr.URL, PullRequestNumber: pr.Number, Title: pr.Title,
			TargetSHA: pr.HeadSHA, Status: state,
		}
		if current := runsByPR[pr.ID]; len(current) > 0 {
			latest := toAOReviewRunResponse(current[0], reviewerHarness)
			response.LatestRun = &latest
			if latest.Status == "running" && current[0].ReviewTerminalID != "" {
				reviewerHandleID = current[0].ReviewTerminalID
			}
			if len(current) > 1 {
				previous := toAOReviewRunResponse(current[1], reviewerHarness)
				response.PreviousRun = &previous
			}
		}
		reviews = append(reviews, response)
	}
	return map[string]any{
		"sessionId":                  sessionID,
		"reviewerHandleId":           reviewerHandleID,
		"reviewerHarness":            reviewerHarness,
		"availableReviewerHarnesses": availableReviewerHarnesses,
		"reviews":                    nonNilReviews(reviews),
		"runs":                       allRuns,
	}, nil
}

func reviewStateForPullRequest(pr domain.PullRequest, runs []domain.ReviewRunPullRequest) string {
	if pr.Draft || pr.State != contract.PRStateOpen || pr.HeadSHA == "" {
		return "ineligible"
	}
	if len(runs) == 0 || runs[0].TargetSHA != pr.HeadSHA {
		return "needs_review"
	}
	latest := runs[0]
	switch latest.Status {
	case contract.AOReviewRunRunning:
		return "running"
	case contract.AOReviewRunDelivered:
		if latest.Verdict == contract.AOReviewVerdictApproved {
			return "up_to_date"
		}
		if latest.Verdict == contract.AOReviewVerdictChangesRequested {
			return "changes_requested"
		}
	}
	return "needs_review"
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Server) getSessionReviewState(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	payload, err := s.sessionReviewPayload(r, orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// triggerSessionReviews starts one reviewer terminal for every open PR in the
// worker session that has not already been reviewed at its current head.
func (s *Server) triggerSessionReviews(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	if s.reviewService == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Starting a review is not available.")
		return
	}
	if err := s.store.CheckSessionWriteAccess(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	session, err := s.store.GetSession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	reviewerHarness := session.ReviewerHarness
	if reviewerHarness == "" {
		reviewerHarness = session.Harness
	}
	if preferences, ok := s.store.(sessionPreferencesStore); ok {
		available, availabilityErr := preferences.AvailableSessionReviewerHarnesses(r.Context(), principalFrom(r), orgID, sessionID)
		if availabilityErr != nil {
			s.writeStoreError(w, r, availabilityErr)
			return
		}
		if !containsString(available, reviewerHarness) {
			writeError(w, r, http.StatusUnprocessableEntity, "REVIEWER_HARNESS_UNAVAILABLE", "The selected reviewer harness is not connected for this session.")
			return
		}
	}
	prs, err := s.store.ListPullRequestsBySession(r.Context(), principalFrom(r), orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	created := false
	startedRunIDs := make([]string, 0, len(prs))
	reviewerHandleID := ""
	for _, pr := range prs {
		if pr.Draft || pr.State != contract.PRStateOpen || pr.HeadSHA == "" {
			continue
		}
		run, didCreate, err := s.reviewService.TriggerReview(r.Context(), orgID, sessionID, reviewerHarness, pr)
		if err != nil {
			s.logger.Error("trigger cloud review", "error", err, "request_id", requestID(r), "pull_request_id", pr.ID)
			if len(startedRunIDs) > 0 {
				if _, rollbackErr := s.reviewService.CancelReviewRuns(r.Context(), orgID, sessionID, startedRunIDs); rollbackErr != nil {
					s.logger.Error("rollback cloud review batch", "error", rollbackErr, "request_id", requestID(r))
				}
			}
			writeError(w, r, http.StatusBadGateway, "REVIEW_FAILED", "The review could not be started.")
			return
		}
		if didCreate {
			startedRunIDs = append(startedRunIDs, run.ID)
		}
		if reviewerHandleID == "" && run.ReviewTerminalID != "" {
			reviewerHandleID = run.ReviewTerminalID
		}
		created = created || didCreate
	}
	payload, err := s.sessionReviewPayload(r, orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if reviewerHandleID != "" {
		payload["reviewerHandleId"] = reviewerHandleID
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, payload)
}

func (s *Server) cancelSessionReviews(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	if s.reviewService == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Cancelling a review is not available.")
		return
	}
	if err := s.store.CheckSessionWriteAccess(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if _, err := s.reviewService.CancelReviews(r.Context(), orgID, sessionID); err != nil {
		s.logger.Error("cancel cloud review", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "REVIEW_FAILED", "The review could not be cancelled.")
		return
	}
	payload, err := s.sessionReviewPayload(r, orgID, sessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func nonNilReviews(reviews []aoPullRequestReviewStateResponse) []aoPullRequestReviewStateResponse {
	if reviews == nil {
		return []aoPullRequestReviewStateResponse{}
	}
	return reviews
}
