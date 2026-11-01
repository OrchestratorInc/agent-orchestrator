package github

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

var _ ports.SCMAttachmentUploader = (*Provider)(nil)

// attachmentsBranch is the branch, in the PR's own repository, that holds
// files attached to PR comments. GitHub's public API cannot attach media to a
// comment, so AO commits the file here and the comment links to it.
const attachmentsBranch = "ao-attachments"

// attachmentRefAttempts bounds how often an upload re-reads the branch after
// losing a race with another upload that moved it first.
const attachmentRefAttempts = 3

// attachmentsReadme is the README of a newly created attachments branch.
const attachmentsReadme = `# ao-attachments

This branch holds files attached to pull request comments from AO (Agent Orchestrator).
Each file lives under ` + "`pr-<number>/`" + ` and is linked from a comment on that pull request.

Do not merge this branch. It shares no history with the rest of the repository,
and deleting its files breaks the comments that link to them.
`

// UploadAttachment commits the file to the ao-attachments branch of the PR's
// repository through the Git Data API and returns a URL GitHub renders inline
// in comments. The first upload creates the branch as an orphan commit with a
// README; later uploads append a commit and fast-forward the branch, retrying
// when a concurrent upload moved it first.
func (p *Provider) UploadAttachment(ctx context.Context, request ports.SCMAttachmentRequest) (ports.SCMAttachmentResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMAttachmentResult{}, fmt.Errorf("github scm: attachment provider is not configured")
	}
	ref := request.PR
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" {
		return ports.SCMAttachmentResult{}, fmt.Errorf("github scm: invalid pull request reference")
	}
	if strings.TrimSpace(request.Name) == "" || strings.ContainsAny(request.Name, `/\`) || len(request.Data) == 0 {
		return ports.SCMAttachmentResult{}, fmt.Errorf("github scm: attachment name and content are required")
	}
	filePath := attachmentPath(ref.Number, p.attachmentTime(), p.attachmentSuffix(), request.Name)
	message := fmt.Sprintf("Attach %s to PR #%d", request.Name, ref.Number)

	// Blobs are content-addressed, so one blob serves every retry below.
	blobSHA, err := p.createAttachmentBlob(ctx, ref.Repo, request.Data)
	if err != nil {
		return ports.SCMAttachmentResult{}, err
	}
	entry := gitTreeEntry{Path: filePath, Mode: "100644", Type: "blob", SHA: blobSHA}

	for attempt := 1; ; attempt++ {
		head, exists, err := p.attachmentsHead(ctx, ref.Repo)
		if err != nil {
			return ports.SCMAttachmentResult{}, err
		}
		if !exists {
			err = p.createAttachmentsBranch(ctx, ref.Repo, entry, message)
		} else {
			err = p.appendAttachment(ctx, ref.Repo, head, entry, message)
		}
		if err == nil {
			return ports.SCMAttachmentResult{URL: attachmentURL(ref, filePath)}, nil
		}
		if !errors.Is(err, errAttachmentRace) || attempt >= attachmentRefAttempts {
			if errors.Is(err, errAttachmentRace) {
				return ports.SCMAttachmentResult{}, fmt.Errorf("%w: %w", ports.ErrSCMRejected, err)
			}
			return ports.SCMAttachmentResult{}, err
		}
	}
}

// errAttachmentRace marks a ref update that lost to a concurrent upload: the
// branch moved (not a fast forward) or was created in the meantime.
var errAttachmentRace = errors.New("github scm: attachments branch moved concurrently")

type gitTreeEntry struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	SHA     string `json:"sha,omitempty"`
	Content string `json:"content,omitempty"`
}

func (p *Provider) createAttachmentBlob(ctx context.Context, repo ports.SCMRepo, data []byte) (string, error) {
	payload := struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}{Content: base64.StdEncoding.EncodeToString(data), Encoding: "base64"}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "blobs"), nil, payload)
	if err != nil {
		return "", mapMutationStatus(resp.StatusCode, err)
	}
	return decodeGitSHA(resp.Body, "blob")
}

// attachmentsHead returns the commit the attachments branch points at, or
// exists=false when the branch has not been created yet. It bypasses the
// client's ETag cache so a retry always sees the branch's current head.
func (p *Provider) attachmentsHead(ctx context.Context, repo ports.SCMRepo) (string, bool, error) {
	resp, err := p.client.doRESTWithETag(ctx, repoPath(repo.Owner, repo.Name, "git", "ref", "heads", attachmentsBranch), nil, "")
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	var out struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", false, fmt.Errorf("github scm: decode attachments ref: %w", err)
	}
	if strings.TrimSpace(out.Object.SHA) == "" {
		return "", false, fmt.Errorf("github scm: attachments ref has no commit")
	}
	return out.Object.SHA, true, nil
}

// createAttachmentsBranch creates the branch as an orphan commit holding the
// README and the first attachment.
func (p *Provider) createAttachmentsBranch(ctx context.Context, repo ports.SCMRepo, entry gitTreeEntry, message string) error {
	treeSHA, err := p.createAttachmentTree(ctx, repo, "", []gitTreeEntry{
		{Path: "README.md", Mode: "100644", Type: "blob", Content: attachmentsReadme},
		entry,
	})
	if err != nil {
		return err
	}
	commitSHA, err := p.createAttachmentCommit(ctx, repo, message, treeSHA, []string{})
	if err != nil {
		return err
	}
	payload := struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}{Ref: "refs/heads/" + attachmentsBranch, SHA: commitSHA}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "refs"), nil, payload)
	if err != nil {
		if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(githubMessage(resp.Body)), "already exists") {
			return fmt.Errorf("%w: %w", errAttachmentRace, err)
		}
		return mapMutationStatus(resp.StatusCode, err)
	}
	return nil
}

// appendAttachment commits entry on top of head and fast-forwards the branch.
func (p *Provider) appendAttachment(ctx context.Context, repo ports.SCMRepo, head string, entry gitTreeEntry, message string) error {
	baseTree, err := p.commitTreeSHA(ctx, repo, head)
	if err != nil {
		return err
	}
	treeSHA, err := p.createAttachmentTree(ctx, repo, baseTree, []gitTreeEntry{entry})
	if err != nil {
		return err
	}
	commitSHA, err := p.createAttachmentCommit(ctx, repo, message, treeSHA, []string{head})
	if err != nil {
		return err
	}
	payload := struct {
		SHA   string `json:"sha"`
		Force bool   `json:"force"`
	}{SHA: commitSHA, Force: false}
	resp, err := p.client.doREST(ctx, http.MethodPatch, repoPath(repo.Owner, repo.Name, "git", "refs", "heads", attachmentsBranch), nil, payload)
	if err != nil {
		if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(githubMessage(resp.Body)), "fast forward") {
			return fmt.Errorf("%w: %w", errAttachmentRace, err)
		}
		return mapMutationStatus(resp.StatusCode, err)
	}
	return nil
}

func (p *Provider) commitTreeSHA(ctx context.Context, repo ports.SCMRepo, commitSHA string) (string, error) {
	resp, err := p.client.doRESTWithETag(ctx, repoPath(repo.Owner, repo.Name, "git", "commits", commitSHA), nil, "")
	if err != nil {
		return "", mapMutationStatus(resp.StatusCode, err)
	}
	var out struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", fmt.Errorf("github scm: decode attachments commit: %w", err)
	}
	if strings.TrimSpace(out.Tree.SHA) == "" {
		return "", fmt.Errorf("github scm: attachments commit has no tree")
	}
	return out.Tree.SHA, nil
}

func (p *Provider) createAttachmentTree(ctx context.Context, repo ports.SCMRepo, baseTree string, entries []gitTreeEntry) (string, error) {
	payload := struct {
		BaseTree string         `json:"base_tree,omitempty"`
		Tree     []gitTreeEntry `json:"tree"`
	}{BaseTree: baseTree, Tree: entries}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "trees"), nil, payload)
	if err != nil {
		return "", mapMutationStatus(resp.StatusCode, err)
	}
	return decodeGitSHA(resp.Body, "tree")
}

func (p *Provider) createAttachmentCommit(ctx context.Context, repo ports.SCMRepo, message, treeSHA string, parents []string) (string, error) {
	payload := struct {
		Message string   `json:"message"`
		Tree    string   `json:"tree"`
		Parents []string `json:"parents"`
	}{Message: message, Tree: treeSHA, Parents: parents}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "commits"), nil, payload)
	if err != nil {
		return "", mapMutationStatus(resp.StatusCode, err)
	}
	return decodeGitSHA(resp.Body, "commit")
}

func decodeGitSHA(body []byte, kind string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github scm: decode %s response: %w", kind, err)
	}
	if strings.TrimSpace(out.SHA) == "" {
		return "", fmt.Errorf("github scm: %s response missing sha", kind)
	}
	return out.SHA, nil
}

// attachmentPath is pr-<number>/<UTC yyyymmdd-hhmmss>-<suffix>-<name>.
func attachmentPath(number int, at time.Time, suffix, name string) string {
	return "pr-" + strconv.Itoa(number) + "/" + at.UTC().Format("20060102-150405") + "-" + suffix + "-" + name
}

// attachmentURL is the blob URL with ?raw=true on the PR's web host, which
// GitHub renders inline in comments, private repositories included for
// viewers who can read the repository.
func attachmentURL(ref ports.SCMPRRef, filePath string) string {
	segments := strings.Split(filePath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "https://" + attachmentHost(ref) + "/" + url.PathEscape(ref.Repo.Owner) + "/" + url.PathEscape(ref.Repo.Name) +
		"/blob/" + attachmentsBranch + "/" + strings.Join(segments, "/") + "?raw=true"
}

// attachmentHost is the web host of the PR: the repository host the observer
// recorded, else the PR URL's host, with API and www prefixes removed.
func attachmentHost(ref ports.SCMPRRef) string {
	host := strings.ToLower(strings.TrimSpace(ref.Repo.Host))
	if host == "" {
		if u, err := url.Parse(ref.URL); err == nil {
			host = strings.ToLower(u.Host)
		}
	}
	host = strings.TrimPrefix(strings.TrimPrefix(host, "api."), "www.")
	if host == "" || !isGitHubHost(host) {
		return "github.com"
	}
	return host
}

func (p *Provider) attachmentTime() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// attachmentSuffix is a short random token that keeps two uploads of the same
// name in the same second apart.
func (p *Provider) attachmentSuffix() string {
	if p.randomSuffix != nil {
		return p.randomSuffix()
	}
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano()%0xffffff, 16)
	}
	return hex.EncodeToString(b[:])
}
