package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	ciFailureLogTailLines       = 20
	reviewDiscussionPageSize    = 100
	reviewCommentLimitPerThread = 5
	pipelineJobsPageSize        = 100
	fetchConcurrency            = 5
)

func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	return p.parseGitLabRepo(remote)
}

func (p *Provider) parseGitLabRepo(remote string) (ports.SCMRepo, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ports.SCMRepo{}, false
	}

	if strings.HasPrefix(remote, "ssh://") {
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return ports.SCMRepo{}, false
		}
		host := u.Host
		if !p.isHostAllowed(host) {
			return ports.SCMRepo{}, false
		}
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		owner, name, ok := splitOwnerRepo(path)
		if !ok {
			return ports.SCMRepo{}, false
		}
		return makeGitLabRepo(host, owner, name), true
	}

	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		host := m[1]
		if !p.isHostAllowed(host) {
			return ports.SCMRepo{}, false
		}
		owner, name, ok := splitOwnerRepo(strings.TrimSuffix(m[2], ".git"))
		if !ok {
			return ports.SCMRepo{}, false
		}
		return makeGitLabRepo(host, owner, name), true
	}

	u, err := url.Parse(remote)
	if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		host := u.Host
		if !p.isHostAllowed(host) {
			return ports.SCMRepo{}, false
		}
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		owner, name, ok := splitOwnerRepo(path)
		if !ok {
			return ports.SCMRepo{}, false
		}
		return makeGitLabRepo(host, owner, name), true
	}

	return ports.SCMRepo{}, false
}

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+)$`)

func splitOwnerRepo(p string) (string, string, bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	for _, seg := range parts {
		if seg == "" {
			return "", "", false
		}
	}
	name := parts[len(parts)-1]
	owner := strings.Join(parts[:len(parts)-1], "/")
	return owner, name, true
}

func makeGitLabRepo(host, owner, name string) ports.SCMRepo {
	return ports.SCMRepo{
		Provider: "gitlab",
		Host:     host,
		Owner:    owner,
		Name:     name,
		Repo:     owner + "/" + name,
	}
}

func (p *Provider) RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(repo.Owner, repo.Name))
	q := url.Values{
		"state":    {"opened"},
		"order_by": {"updated_at"},
		"sort":     {"desc"},
		"per_page": {"1"},
	}
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}

	resp, err := hc.doRESTWithETag(ctx, path, q, etag)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return ports.SCMGuardResult{
		ETag:        resp.ETag,
		NotModified: resp.NotModified,
	}, nil
}

func (p *Provider) ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error) {
	var result []ports.SCMPRObservation
	q := url.Values{
		"state":    {"all"},
		"order_by": {"updated_at"},
		"sort":     {"desc"},
		"per_page": {"100"},
	}
	if !updatedAfter.IsZero() {
		q.Set("updated_after", updatedAfter.UTC().Format(time.RFC3339Nano))
	}
	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(repo.Owner, repo.Name))
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, err
	}

	_, err = hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var mrs []restMR
		if err := json.Unmarshal(body, &mrs); err != nil {
			return fmt.Errorf("gitlab scm: unmarshal MR list: %w", err)
		}
		for i := range mrs {
			p.cache.setMRDetail(repo.Host, repo.Repo, mrs[i].IID, &mrs[i])
			result = append(result, mrToSCMPRObservation(repo, &mrs[i]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Provider) ListOpenPRsByBranch(ctx context.Context, repo ports.SCMRepo, branch string) ([]ports.SCMPRObservation, error) {
	q := url.Values{
		"state":         {"opened"},
		"source_branch": {branch},
		"per_page":      {"100"},
	}
	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(repo.Owner, repo.Name))
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, err
	}
	var result []ports.SCMPRObservation
	_, err = hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var mrs []restMR
		if err := json.Unmarshal(body, &mrs); err != nil {
			return fmt.Errorf("gitlab scm: unmarshal branch MR list: %w", err)
		}
		for i := range mrs {
			if mrs[i].SourceProjectID != 0 && mrs[i].SourceProjectID != mrs[i].TargetProjectID {
				continue
			}
			obs := mrToSCMPRObservation(repo, &mrs[i])
			obs.BaseRepo = repo.Repo
			result = append(result, obs)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type restMR struct {
	ID                  int64  `json:"id"`
	IID                 int    `json:"iid"`
	Title               string `json:"title"`
	State               string `json:"state"`
	Draft               bool   `json:"draft"`
	WebURL              string `json:"web_url"`
	SourceBranch        string `json:"source_branch"`
	TargetBranch        string `json:"target_branch"`
	SHA                 string `json:"sha"`
	MergeStatus         string `json:"merge_status"`
	DetailedMergeStatus string `json:"detailed_merge_status"`

	Author struct {
		Username string `json:"username"`
	} `json:"author"`

	SourceProjectID int `json:"source_project_id"`
	TargetProjectID int `json:"target_project_id"`

	DiffRefs       restDiffRefs `json:"diff_refs"`
	MergeCommitSHA string       `json:"merge_commit_sha"`

	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
	ClosedAt  *time.Time `json:"closed_at"`

	BlockingDiscussionsResolved bool `json:"blocking_discussions_resolved"`
}

type restProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
}

type restDiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

func mrToSCMPRObservation(repo ports.SCMRepo, mr *restMR) ports.SCMPRObservation {
	state := normalizeMRState(mr.State, mr.Draft)
	merged := mr.State == "merged"
	closed := mr.State == "closed" || mr.State == "locked"
	return ports.SCMPRObservation{
		ProviderID:               providerID(mr.ID),
		URL:                      mr.WebURL,
		HTMLURL:                  mr.WebURL,
		Number:                   mr.IID,
		State:                    string(state),
		Draft:                    mr.Draft,
		Merged:                   merged,
		Closed:                   closed,
		SourceBranch:             mr.SourceBranch,
		HeadRepo:                 repo.Repo,
		TargetBranch:             mr.TargetBranch,
		HeadSHA:                  mr.SHA,
		Title:                    mr.Title,
		Author:                   mr.Author.Username,
		ProviderState:            mr.State,
		ProviderMergeable:        mr.MergeStatus,
		ProviderMergeStateStatus: effectiveMergeStatus(mr),
		CreatedAtProvider:        safeTime(mr.CreatedAt),
		UpdatedAtProvider:        safeTime(mr.UpdatedAt),
		MergedAtProvider:         safeTime(mr.MergedAt),
		ClosedAtProvider:         safeTime(mr.ClosedAt),
	}
}

func providerID(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func normalizeMRState(state string, draft bool) domain.PRState {
	switch state {
	case "opened":
		if draft {
			return domain.PRStateDraft
		}
		return domain.PRStateOpen
	case "merged":
		return domain.PRStateMerged
	case "closed", "locked":
		return domain.PRStateClosed
	default:
		return domain.PRStateClosed
	}
}

func effectiveMergeStatus(mr *restMR) string {
	if mr.DetailedMergeStatus != "" {
		return mr.DetailedMergeStatus
	}
	return mr.MergeStatus
}

func changesRequested(mr *restMR) bool {
	return effectiveMergeStatus(mr) == "requested_changes"
}

func safeTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (p *Provider) CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error) {
	if headSHA == "" {
		return ports.SCMGuardResult{}, fmt.Errorf("gitlab scm: empty head SHA: %w", ErrNotFound)
	}
	path := fmt.Sprintf("/projects/%s/pipelines", projectPath(repo.Owner, repo.Name))
	q := url.Values{
		"sha":      {headSHA},
		"per_page": {"1"},
		"order_by": {"id"},
		"sort":     {"desc"},
	}
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}

	resp, err := hc.doGET(ctx, path, q)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	h := sha256.Sum256(resp.Body)
	newETag := hex.EncodeToString(h[:])
	return ports.SCMGuardResult{
		ETag:        newETag,
		NotModified: etag == newETag,
	}, nil
}

func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) > 25 {
		return nil, fmt.Errorf("gitlab scm: batch size %d exceeds limit 25", len(refs))
	}
	results := make([]ports.SCMObservation, len(refs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetchConcurrency)
	var firstErr error

	for i, ref := range refs {
		wg.Add(1)
		go func(idx int, r ports.SCMPRRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			obs, err := p.fetchSingleMR(ctx, r)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				p.logger.Warn("gitlab scm: fetch MR failed", "repo", r.Repo.Repo, "mr", r.Number, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				results[idx] = ports.SCMObservation{
					Fetched:  false,
					Provider: "gitlab",
					Host:     r.Repo.Host,
					Repo:     r.Repo.Repo,
					PR:       ports.SCMPRObservation{Number: r.Number, URL: r.URL},
					Error:    err,
				}
				return
			}
			results[idx] = obs
		}(i, ref)
	}
	wg.Wait()
	return results, firstErr
}

func (p *Provider) fetchSingleMR(ctx context.Context, ref ports.SCMPRRef) (ports.SCMObservation, error) {
	repo := ref.Repo
	now := time.Now()

	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMObservation{}, err
	}

	var mr restMR
	if cached, ok := p.cache.getMRDetail(repo.Host, repo.Repo, ref.Number); ok && cached.DiffRefs.BaseSHA != "" {
		mr = *cached
	} else {
		mrPath := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(repo.Owner, repo.Name), ref.Number)
		mrResp, err := hc.doGET(ctx, mrPath, nil)
		if err != nil {
			return ports.SCMObservation{}, err
		}
		if err := json.Unmarshal(mrResp.Body, &mr); err != nil {
			return ports.SCMObservation{}, fmt.Errorf("gitlab scm: unmarshal MR detail: %w", err)
		}
	}

	prObs := mrToSCMPRObservation(repo, &mr)
	canonicalRepo := canonicalRepoFromMRURL(repo.Host, prObs.URL, repo.Repo)
	if mr.SourceProjectID == 0 || mr.SourceProjectID == mr.TargetProjectID {
		prObs.HeadRepo = canonicalRepo
	}
	if requestedURL := strings.TrimSpace(ref.URL); requestedURL != "" && requestedURL != strings.TrimSpace(prObs.URL) {
		prObs.URLAlias = requestedURL
	}
	prObs.BaseSHA = mr.DiffRefs.BaseSHA
	prObs.MergeCommitSHA = mr.MergeCommitSHA

	if mr.SourceProjectID != 0 && mr.SourceProjectID != mr.TargetProjectID {
		path, err := p.fetchSourceProjectCached(ctx, hc, repo.Host, mr.SourceProjectID)
		if err != nil {
			return ports.SCMObservation{}, fmt.Errorf("gitlab scm: fetch source project %d: %w", mr.SourceProjectID, err)
		}
		if path != "" {
			prObs.HeadRepo = path
		}
	}

	ciObs, err := p.fetchCI(ctx, repo, mr.SHA)
	if err != nil {
		return ports.SCMObservation{}, fmt.Errorf("gitlab scm: fetch CI: %w", err)
	}

	reviewDecision, err := p.fetchApprovalDecision(ctx, repo, ref.Number)
	if err != nil {
		return ports.SCMObservation{}, fmt.Errorf("gitlab scm: fetch approvals: %w", err)
	}

	if changesRequested(&mr) {
		reviewDecision = domain.ReviewChangesRequest
	}

	mergeObs := mergeabilityFromMR(&mr, ciObs.Summary, string(reviewDecision))

	return ports.SCMObservation{
		Fetched:      true,
		ObservedAt:   now,
		Provider:     "gitlab",
		Host:         repo.Host,
		Repo:         canonicalRepo,
		PR:           prObs,
		CI:           ciObs,
		Review:       ports.SCMReviewObservation{Decision: string(reviewDecision)},
		Mergeability: mergeObs,
	}, nil
}

func canonicalRepoFromMRURL(host, rawURL, fallback string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(u.Host, strings.TrimSpace(host)) {
		return fallback
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 1; i+1 < len(parts); i++ {
		if parts[i] == "-" && parts[i+1] == "merge_requests" {
			return strings.Join(parts[:i], "/")
		}
	}
	return fallback
}

func (p *Provider) fetchSourceProjectCached(ctx context.Context, hc *Client, host string, sourceProjectID int) (string, error) {
	if path, ok := p.cache.getForkProject(host, sourceProjectID); ok {
		return path, nil
	}
	srcPath := fmt.Sprintf("/projects/%d", sourceProjectID)
	projResp, err := hc.doGET(ctx, srcPath, nil)
	if err != nil {
		return "", err
	}
	var srcProj restProject
	if err := json.Unmarshal(projResp.Body, &srcProj); err != nil {
		return "", fmt.Errorf("gitlab scm: unmarshal source project %d: %w", sourceProjectID, err)
	}
	p.cache.setForkProject(host, sourceProjectID, srcProj.PathWithNamespace)
	return srcProj.PathWithNamespace, nil
}

func (p *Provider) fetchCI(ctx context.Context, repo ports.SCMRepo, headSHA string) (ports.SCMCIObservation, error) {
	if headSHA == "" {
		return ports.SCMCIObservation{Summary: string(domain.CIUnknown)}, nil
	}

	path := fmt.Sprintf("/projects/%s/pipelines", projectPath(repo.Owner, repo.Name))
	q := url.Values{
		"sha":      {headSHA},
		"per_page": {"1"},
		"order_by": {"id"},
		"sort":     {"desc"},
	}
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMCIObservation{}, err
	}

	resp, err := hc.doGET(ctx, path, q)
	if err != nil {
		return ports.SCMCIObservation{}, fmt.Errorf("fetch pipelines: %w", err)
	}

	var pipelines []restPipeline
	if err := json.Unmarshal(resp.Body, &pipelines); err != nil {
		return ports.SCMCIObservation{}, fmt.Errorf("unmarshal pipelines: %w", err)
	}
	if len(pipelines) == 0 {
		return ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: headSHA}, nil
	}

	latest := pipelines[0]
	ciState := pipelineStatusToCI(latest.Status)

	checks, failedChecks, partial, err := p.fetchPipelineJobs(ctx, repo, latest.ID)
	if err != nil {
		return ports.SCMCIObservation{}, fmt.Errorf("fetch pipeline jobs: %w", err)
	}

	if latest.Status == "canceled" && len(failedChecks) > 0 {
		ciState = domain.CIFailing
	}

	fp := gitlabFailedFingerprint(headSHA, failedChecks)

	return ports.SCMCIObservation{
		Summary:           string(ciState),
		HeadSHA:           headSHA,
		FailedFingerprint: fp,
		Checks:            checks,
		FailedChecks:      failedChecks,
		Partial:           partial,
	}, nil
}

type restPipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	SHA    string `json:"sha"`
}

func (p *Provider) fetchPipelineJobs(ctx context.Context, repo ports.SCMRepo, pipelineID int) ([]ports.SCMCheckObservation, []ports.SCMCheckObservation, bool, error) {
	path := fmt.Sprintf("/projects/%s/pipelines/%d/jobs", projectPath(repo.Owner, repo.Name), pipelineID)
	q := url.Values{"per_page": {strconv.Itoa(pipelineJobsPageSize)}}
	var allJobs []restJob
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, nil, false, err
	}

	truncated, err := hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var jobs []restJob
		if err := json.Unmarshal(body, &jobs); err != nil {
			return fmt.Errorf("unmarshal pipeline jobs: %w", err)
		}
		allJobs = append(allJobs, jobs...)
		return nil
	})
	if err != nil {
		return nil, nil, false, fmt.Errorf("fetch pipeline jobs: %w", err)
	}

	var checks, failed []ports.SCMCheckObservation
	for _, j := range allJobs {
		status := jobStatusToCheckStatus(j.Status)
		check := ports.SCMCheckObservation{
			Name:       j.Name,
			Status:     string(status),
			Conclusion: j.Status,
			URL:        j.WebURL,
			ProviderID: strconv.Itoa(j.ID),
		}
		checks = append(checks, check)
		if isFailingCheckStatus(status) && !j.AllowFailure {
			failed = append(failed, check)
		}
	}
	return checks, failed, truncated, nil
}

type restJob struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	WebURL       string `json:"web_url"`
	AllowFailure bool   `json:"allow_failure"`
}

func (p *Provider) fetchApprovalDecision(ctx context.Context, repo ports.SCMRepo, mrIID int) (domain.ReviewDecision, error) {
	approvals, err := p.fetchApprovalsCached(ctx, repo, mrIID)
	if err != nil {
		return domain.ReviewNone, err
	}
	return approvalDecision(*approvals), nil
}

func (p *Provider) fetchApprovalsCached(ctx context.Context, repo ports.SCMRepo, mrIID int) (*restApprovals, error) {
	if cached, ok := p.cache.getApprovals(repo.Host, repo.Repo, mrIID); ok {
		return cached, nil
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/approvals", projectPath(repo.Owner, repo.Name), mrIID)
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, err
	}

	resp, err := hc.doGET(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch approvals: %w", err)
	}

	var approvals restApprovals
	if err := json.Unmarshal(resp.Body, &approvals); err != nil {
		return nil, fmt.Errorf("unmarshal approvals: %w", err)
	}
	p.cache.setApprovals(repo.Host, repo.Repo, mrIID, &approvals)
	return &approvals, nil
}

func approvalDecision(a restApprovals) domain.ReviewDecision {
	if a.Approved {
		if a.ApprovalsRequired > 0 || len(a.ApprovedBy) > 0 {
			return domain.ReviewApproved
		}
		return domain.ReviewNone
	}
	if a.ApprovalsRequired > 0 {
		return domain.ReviewRequired
	}
	return domain.ReviewNone
}

type restApprovals struct {
	Approved          bool `json:"approved"`
	ApprovalsRequired int  `json:"approvals_required"`
	ApprovedBy        []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		ApprovedAt *time.Time `json:"approved_at"`
	} `json:"approved_by"`
}

func (p *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	jobID := check.ProviderID
	if jobID == "" {
		return "", fmt.Errorf("gitlab scm: empty job ID")
	}
	path := fmt.Sprintf("/projects/%s/jobs/%s/trace", projectPath(repo.Owner, repo.Name), jobID)
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return "", err
	}

	body, err := hc.doGETRaw(ctx, path)
	if err != nil {
		return "", err
	}
	return tailLines(string(body), ciFailureLogTailLines), nil
}

func (p *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	repo := ref.Repo

	path := fmt.Sprintf("/projects/%s/merge_requests/%d/discussions", projectPath(repo.Owner, repo.Name), ref.Number)
	q := url.Values{"per_page": {strconv.Itoa(reviewDiscussionPageSize)}}
	var discussions []restDiscussion
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}

	truncated, err := hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var page []restDiscussion
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("gitlab scm: unmarshal discussions: %w", err)
		}
		discussions = append(discussions, page...)
		return nil
	})
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}

	var threads []ports.SCMReviewThreadObservation
	for _, d := range discussions {
		if d.IndividualNote {
			continue
		}
		if len(d.Notes) == 0 {
			continue
		}
		firstNote := d.Notes[0]
		if firstNote.System {
			continue
		}

		resolved := true
		allBot := true
		var comments []ports.SCMReviewCommentObservation
		filePath := ""
		line := 0

		for j, n := range d.Notes {
			if n.System {
				continue
			}
			isBot := isBotAuthor(n.Author.Username)
			if !isBot {
				allBot = false
			}
			if !n.Resolved && n.Resolvable {
				resolved = false
			}
			if j == 0 && n.Position != nil {
				filePath = n.Position.NewPath
				line = n.Position.NewLine
			}
			if j < reviewCommentLimitPerThread {
				comments = append(comments, ports.SCMReviewCommentObservation{
					ID:        strconv.Itoa(n.ID),
					Author:    n.Author.Username,
					Body:      n.Body,
					URL:       n.noteURL(ref),
					IsBot:     isBot,
					CreatedAt: safeTime(n.CreatedAt),
				})
			}
		}

		threads = append(threads, ports.SCMReviewThreadObservation{
			ID:       d.ID,
			Path:     filePath,
			Line:     line,
			Resolved: resolved,
			IsBot:    allBot,
			Comments: comments,
		})
	}

	approvalsPtr, err := p.fetchApprovalsCached(ctx, repo, ref.Number)
	if err != nil {
		return ports.SCMReviewObservation{}, fmt.Errorf("gitlab scm: %w", err)
	}
	approvals := *approvalsPtr
	decision := approvalDecision(approvals)

	if cached, ok := p.cache.getMRDetail(repo.Host, repo.Repo, ref.Number); ok {
		if changesRequested(cached) {
			decision = domain.ReviewChangesRequest
		}
	}

	var reviews []ports.SCMReviewSummaryObservation
	for _, ab := range approvals.ApprovedBy {
		username := ab.User.Username
		reviews = append(reviews, ports.SCMReviewSummaryObservation{
			ID:          "approval:" + username,
			Author:      username,
			State:       string(domain.ReviewApproved),
			IsBot:       isBotAuthor(username),
			SubmittedAt: safeTime(ab.ApprovedAt),
		})
	}

	return ports.SCMReviewObservation{
		Decision: string(decision),
		Reviews:  reviews,
		Threads:  threads,
		Partial:  truncated,
	}, nil
}

type restDiscussion struct {
	ID             string     `json:"id"`
	IndividualNote bool       `json:"individual_note"`
	Notes          []restNote `json:"notes"`
}

type restNote struct {
	ID         int    `json:"id"`
	Body       string `json:"body"`
	System     bool   `json:"system"`
	Resolvable bool   `json:"resolvable"`
	Resolved   bool   `json:"resolved"`
	Author     struct {
		Username string `json:"username"`
	} `json:"author"`
	Position  *restNotePosition `json:"position"`
	CreatedAt *time.Time        `json:"created_at"`
}

type restNotePosition struct {
	NewPath string `json:"new_path"`
	NewLine int    `json:"new_line"`
}

func (n *restNote) noteURL(ref ports.SCMPRRef) string {
	return fmt.Sprintf("%s#note_%d", ref.URL, n.ID)
}

func pipelineStatusToCI(status string) domain.CIState {
	switch status {
	case "success":
		return domain.CIPassing
	case "failed":
		return domain.CIFailing
	case "running", "pending", "created", "waiting_for_resource", "preparing", "scheduled":
		return domain.CIPending
	default:
		return domain.CIUnknown
	}
}

func jobStatusToCheckStatus(status string) domain.PRCheckStatus {
	switch status {
	case "success":
		return domain.PRCheckPassed
	case "failed":
		return domain.PRCheckFailed
	case "canceled":
		return domain.PRCheckCancelled
	case "running", "preparing":
		return domain.PRCheckInProgress
	case "pending", "created", "waiting_for_resource", "scheduled":
		return domain.PRCheckQueued
	case "skipped", "manual":
		return domain.PRCheckSkipped
	default:
		return domain.PRCheckUnknown
	}
}

func isFailingCheckStatus(s domain.PRCheckStatus) bool {
	return s == domain.PRCheckFailed
}

func mergeabilityFromMR(mr *restMR, ciState, reviewDecision string) ports.SCMMergeabilityObservation {
	ms := effectiveMergeStatus(mr)
	var blockers []string

	switch ms {
	case "mergeable", "can_be_merged":
		mergeable := true
		if ciState == string(domain.CIFailing) {
			blockers = append(blockers, "ci_failing")
			mergeable = false
		}
		if reviewDecision == string(domain.ReviewRequired) {
			blockers = append(blockers, "review_required")
			mergeable = false
		}
		if mr.Draft {
			blockers = append(blockers, "draft")
			mergeable = false
		}
		if mergeable {
			return ports.SCMMergeabilityObservation{
				State:     string(domain.MergeMergeable),
				Mergeable: true,
			}
		}
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: blockers,
		}

	case "conflict", "cannot_be_merged", "cannot_be_merged_recheck":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeConflicting),
			Conflict: true,
			Blockers: []string{"conflicts"},
		}

	case "checking", "preparing", "unchecked", "approvals_syncing", "":
		return ports.SCMMergeabilityObservation{
			State: string(domain.MergeUnknown),
		}

	case "need_rebase":
		return ports.SCMMergeabilityObservation{
			State:      string(domain.MergeBlocked),
			BehindBase: true,
			Blockers:   []string{"behind_base"},
		}

	case "ci_must_pass", "ci_still_running":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"ci_failing"},
		}

	case "discussions_not_resolved":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"discussions_unresolved"},
		}

	case "draft_status":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"draft"},
		}

	case "requested_changes":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"changes_requested"},
		}

	case "not_approved":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"review_required"},
		}

	case "not_open", "merge_request_blocked", "merge_time", "commits_status",
		"jira_association_missing", "status_checks_must_pass",
		"security_policy_pipeline_check", "security_policy_violations",
		"locked_paths", "locked_lfs_files", "title_regex":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"blocked_by_provider"},
		}

	default:
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"blocked_by_provider"},
		}
	}
}

var botUsernameRe = regexp.MustCompile(`^project_\d+_bot`)

func isBotAuthor(username string) bool {
	if strings.HasSuffix(username, "[bot]") || strings.HasSuffix(username, "-bot") {
		return true
	}
	switch username {
	case "gitlab-bot", "ghost", "dependabot[bot]", "renovate[bot]",
		"sast-bot", "codeclimate[bot]", "sonarcloud[bot]", "snyk-bot":
		return true
	}
	return botUsernameRe.MatchString(username)
}

func gitlabFailedFingerprint(headSHA string, checks []ports.SCMCheckObservation) string {
	if len(checks) == 0 {
		return ""
	}
	parts := make([]string, len(checks))
	for i, c := range checks {
		parts[i] = headSHA + "\x00" + c.Name + "\x00" + c.Status + "\x00" + c.Conclusion + "\x00" + c.URL + "\x00" + c.ProviderID
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(h[:])
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
