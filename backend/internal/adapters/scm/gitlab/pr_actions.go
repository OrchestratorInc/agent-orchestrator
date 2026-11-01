package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMPRStateChanger = (*Provider)(nil)
var _ ports.SCMCommenter = (*Provider)(nil)

// ChangePullRequestState closes or reopens a GitLab merge request through its
// state_event. GitLab only expresses draft status through the title prefix,
// so ready and draft are unsupported.
func (p *Provider) ChangePullRequestState(ctx context.Context, request ports.SCMPRStateRequest) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("gitlab scm: state provider is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return fmt.Errorf("gitlab scm: invalid merge request reference")
	}
	var event, want string
	switch request.Action {
	case ports.SCMPRClose:
		event, want = "close", "closed"
	case ports.SCMPRReopen:
		event, want = "reopen", "opened"
	default:
		return fmt.Errorf("%w: gitlab merge request state action %q", ports.ErrSCMUnsupported, request.Action)
	}
	client, err := p.clientForRepoErr(request.PR.Repo)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(request.PR.Repo.Owner, request.PR.Repo.Name), request.PR.Number)
	resp, err := client.doMERGE(ctx, path, nil, struct {
		StateEvent string `json:"state_event"`
	}{StateEvent: event})
	if err != nil {
		return mapMutationStatus(resp.StatusCode, err)
	}
	var mr struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(resp.Body, &mr); err != nil {
		return fmt.Errorf("gitlab scm: decode merge request state response: %w", err)
	}
	if mr.State != want {
		return fmt.Errorf("%w: merge request state is %q, want %q", ports.ErrSCMRejected, mr.State, want)
	}
	return nil
}

// PostComment posts a top-level merge-request note, or a reply note in the
// discussion named by request.ThreadID.
func (p *Provider) PostComment(ctx context.Context, request ports.SCMCommentRequest) (ports.SCMCommentResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMCommentResult{}, fmt.Errorf("gitlab scm: comment provider is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return ports.SCMCommentResult{}, fmt.Errorf("gitlab scm: invalid merge request reference")
	}
	if strings.TrimSpace(request.Body) == "" {
		return ports.SCMCommentResult{}, fmt.Errorf("gitlab scm: comment body is required")
	}
	client, err := p.clientForRepoErr(request.PR.Repo)
	if err != nil {
		return ports.SCMCommentResult{}, err
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(request.PR.Repo.Owner, request.PR.Repo.Name), request.PR.Number)
	if threadID := strings.TrimSpace(request.ThreadID); threadID != "" {
		path += "/discussions/" + url.PathEscape(threadID) + "/notes"
	} else {
		path += "/notes"
	}
	resp, err := client.doPOST(ctx, path, nil, struct {
		Body string `json:"body"`
	}{Body: request.Body})
	if err != nil {
		return ports.SCMCommentResult{}, mapMutationStatus(resp.StatusCode, err)
	}
	var note struct {
		ID        int        `json:"id"`
		Body      string     `json:"body"`
		CreatedAt *time.Time `json:"created_at"`
		Author    struct {
			Username string `json:"username"`
		} `json:"author"`
	}
	if err := json.Unmarshal(resp.Body, &note); err != nil {
		return ports.SCMCommentResult{}, fmt.Errorf("gitlab scm: decode note response: %w", err)
	}
	if note.ID <= 0 {
		return ports.SCMCommentResult{}, fmt.Errorf("gitlab scm: note response missing id")
	}
	body := note.Body
	if body == "" {
		body = request.Body
	}
	return ports.SCMCommentResult{
		ID:        strconv.Itoa(note.ID),
		URL:       (&restNote{ID: note.ID}).noteURL(request.PR),
		Author:    note.Author.Username,
		IsBot:     isBotAuthor(note.Author.Username),
		Body:      body,
		CreatedAt: safeTime(note.CreatedAt),
	}, nil
}

// mapMutationStatus classifies a failed merge-request mutation: 404 is an
// unknown MR and 400/405/409/422 are GitLab refusing it.
func mapMutationStatus(status int, err error) error {
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
	case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusConflict, http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %w", ports.ErrSCMRejected, err)
	default:
		return err
	}
}

var _ ports.SCMAttachmentUploader = (*Provider)(nil)

// UploadAttachment stores the file with GitLab's project uploads API. GitLab
// serves it from the project and returns the markdown that embeds it in a
// note, so no branch is involved. The returned URL is absolute on the PR's
// web host.
func (p *Provider) UploadAttachment(ctx context.Context, request ports.SCMAttachmentRequest) (ports.SCMAttachmentResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMAttachmentResult{}, fmt.Errorf("gitlab scm: attachment provider is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return ports.SCMAttachmentResult{}, fmt.Errorf("gitlab scm: invalid merge request reference")
	}
	if strings.TrimSpace(request.Name) == "" || len(request.Data) == 0 {
		return ports.SCMAttachmentResult{}, fmt.Errorf("gitlab scm: attachment name and content are required")
	}
	client, err := p.clientForRepoErr(request.PR.Repo)
	if err != nil {
		return ports.SCMAttachmentResult{}, err
	}
	path := fmt.Sprintf("/projects/%s/uploads", projectPath(request.PR.Repo.Owner, request.PR.Repo.Name))
	resp, err := client.doPOSTFile(ctx, path, request.Name, request.ContentType, request.Data)
	if err != nil {
		return ports.SCMAttachmentResult{}, mapMutationStatus(resp.StatusCode, err)
	}
	var upload struct {
		URL      string `json:"url"`
		FullPath string `json:"full_path"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(resp.Body, &upload); err != nil {
		return ports.SCMAttachmentResult{}, fmt.Errorf("gitlab scm: decode upload response: %w", err)
	}
	if strings.TrimSpace(upload.URL) == "" && strings.TrimSpace(upload.FullPath) == "" {
		return ports.SCMAttachmentResult{}, fmt.Errorf("gitlab scm: upload response missing url")
	}
	return ports.SCMAttachmentResult{URL: uploadURL(request.PR, upload.URL, upload.FullPath), Markdown: upload.Markdown}, nil
}

// uploadURL makes an upload's URL absolute. full_path is already rooted at
// the instance; url is relative to the project, so it gets the project path.
func uploadURL(ref ports.SCMPRRef, relative, fullPath string) string {
	if u, err := url.Parse(fullPath); err == nil && u.IsAbs() {
		return fullPath
	}
	base := "https://" + NormalizeHost(ref.Repo.Host)
	if u, err := url.Parse(ref.URL); err == nil && u.Scheme != "" && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	} else if IsGitLabDotCom(ref.Repo.Host) {
		base = "https://gitlab.com"
	}
	if fullPath = strings.TrimSpace(fullPath); fullPath != "" {
		return base + "/" + strings.TrimPrefix(fullPath, "/")
	}
	return base + "/" + ref.Repo.Owner + "/" + ref.Repo.Name + "/" + strings.TrimPrefix(relative, "/")
}
