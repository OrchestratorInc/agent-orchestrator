package github

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMAttachmentStore = (*Provider)(nil)

const attachmentsRefPrefix = "refs/ao/attachments/"

func (p *Provider) AttachmentAccess(ctx context.Context, ref ports.SCMPRRef) (ports.SCMAttachmentAccess, error) {
	if p == nil || p.client == nil {
		return ports.SCMAttachmentAccess{}, fmt.Errorf("github scm: attachment provider is not configured")
	}
	if err := validAttachmentRef(ref); err != nil {
		return ports.SCMAttachmentAccess{}, err
	}
	location := ref.Repo.Owner + "/" + ref.Repo.Name
	resp, err := p.client.doRESTWithETag(ctx, repoPath(ref.Repo.Owner, ref.Repo.Name), nil, "")
	if err != nil {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
			return ports.SCMAttachmentAccess{Location: location, Reason: ports.SCMAttachmentNoWriteAccess}, nil
		}
		return ports.SCMAttachmentAccess{}, err
	}
	var repo struct {
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(resp.Body, &repo); err != nil {
		return ports.SCMAttachmentAccess{}, fmt.Errorf("github scm: decode repository: %w", err)
	}
	if !repo.Permissions.Push {
		return ports.SCMAttachmentAccess{Location: location, Reason: ports.SCMAttachmentNoWriteAccess}, nil
	}
	return ports.SCMAttachmentAccess{Allowed: true, Location: location}, nil
}

func (p *Provider) StoreAttachments(ctx context.Context, batch ports.SCMAttachmentBatch) (ports.SCMStoredAttachments, error) {
	if p == nil || p.client == nil {
		return ports.SCMStoredAttachments{}, fmt.Errorf("github scm: attachment provider is not configured")
	}
	ref := batch.PR
	if err := validAttachmentRef(ref); err != nil {
		return ports.SCMStoredAttachments{}, err
	}
	if len(batch.Files) == 0 {
		return ports.SCMStoredAttachments{}, fmt.Errorf("github scm: no attachments to store")
	}
	paths := attachmentPaths(batch.Files)
	entries := make([]gitTreeEntry, 0, len(batch.Files))
	for i, file := range batch.Files {
		if strings.TrimSpace(file.Name) == "" || strings.ContainsAny(file.Name, `/\`) || len(file.Data) == 0 {
			return ports.SCMStoredAttachments{}, fmt.Errorf("github scm: attachment name and content are required")
		}
		blobSHA, err := p.createAttachmentBlob(ctx, ref.Repo, file.Data)
		if err != nil {
			return ports.SCMStoredAttachments{}, err
		}
		entries = append(entries, gitTreeEntry{Path: paths[i], Mode: "100644", Type: "blob", SHA: blobSHA})
	}
	treeSHA, err := p.createAttachmentTree(ctx, ref.Repo, entries)
	if err != nil {
		return ports.SCMStoredAttachments{}, err
	}
	message := fmt.Sprintf("Attach files to PR #%d [skip ci]\n\nFiles attached to a pull request comment from AO (Agent Orchestrator).", ref.Number)
	commitSHA, err := p.createAttachmentCommit(ctx, ref.Repo, message, treeSHA)
	if err != nil {
		return ports.SCMStoredAttachments{}, err
	}
	handle := attachmentsRefPrefix + "pr-" + strconv.Itoa(ref.Number) + "/" + p.attachmentTime().UTC().Format("20060102-150405") + "-" + p.attachmentSuffix()
	payload := struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}{Ref: handle, SHA: commitSHA}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(ref.Repo.Owner, ref.Repo.Name, "git", "refs"), nil, payload)
	if err != nil {
		return ports.SCMStoredAttachments{}, attachmentStatus(resp.StatusCode, err)
	}
	urls := make([]string, len(paths))
	for i, filePath := range paths {
		urls[i] = attachmentURL(ref, commitSHA, filePath)
	}
	return ports.SCMStoredAttachments{URLs: urls, Handle: handle}, nil
}

func (p *Provider) DiscardAttachments(ctx context.Context, ref ports.SCMPRRef, handle string) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github scm: attachment provider is not configured")
	}
	if err := validAttachmentRef(ref); err != nil {
		return err
	}
	name, ok := strings.CutPrefix(handle, attachmentsRefPrefix)
	if !ok || name == "" || strings.Contains(name, "..") {
		return fmt.Errorf("github scm: %q is not an attachments ref", handle)
	}
	elems := append([]string{"git", "refs", "ao", "attachments"}, strings.Split(name, "/")...)
	resp, err := p.client.doREST(ctx, http.MethodDelete, repoPath(ref.Repo.Owner, ref.Repo.Name, elems...), nil, nil)
	if err != nil {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity {
			return nil
		}
		return attachmentStatus(resp.StatusCode, err)
	}
	return nil
}

func validAttachmentRef(ref ports.SCMPRRef) error {
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" {
		return fmt.Errorf("github scm: invalid pull request reference")
	}
	return nil
}

func attachmentStatus(status int, err error) error {
	switch status {
	case http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
	default:
		return mapMutationStatus(status, err)
	}
}

type gitTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (p *Provider) createAttachmentBlob(ctx context.Context, repo ports.SCMRepo, data []byte) (string, error) {
	payload := struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}{Content: base64.StdEncoding.EncodeToString(data), Encoding: "base64"}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "blobs"), nil, payload)
	if err != nil {
		return "", attachmentStatus(resp.StatusCode, err)
	}
	return decodeGitSHA(resp.Body, "blob")
}

func (p *Provider) createAttachmentTree(ctx context.Context, repo ports.SCMRepo, entries []gitTreeEntry) (string, error) {
	payload := struct {
		Tree []gitTreeEntry `json:"tree"`
	}{Tree: entries}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "trees"), nil, payload)
	if err != nil {
		return "", attachmentStatus(resp.StatusCode, err)
	}
	return decodeGitSHA(resp.Body, "tree")
}

func (p *Provider) createAttachmentCommit(ctx context.Context, repo ports.SCMRepo, message, treeSHA string) (string, error) {
	payload := struct {
		Message string   `json:"message"`
		Tree    string   `json:"tree"`
		Parents []string `json:"parents"`
	}{Message: message, Tree: treeSHA, Parents: []string{}}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(repo.Owner, repo.Name, "git", "commits"), nil, payload)
	if err != nil {
		return "", attachmentStatus(resp.StatusCode, err)
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

func attachmentPaths(files []ports.SCMAttachmentFile) []string {
	paths := make([]string, len(files))
	seen := map[string]bool{}
	for i, file := range files {
		name := file.Name
		for n := 2; seen[name]; n++ {
			name = strconv.Itoa(n) + "-" + file.Name
		}
		seen[name] = true
		paths[i] = name
	}
	return paths
}

func attachmentURL(ref ports.SCMPRRef, commitSHA, filePath string) string {
	segments := strings.Split(filePath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "https://" + attachmentHost(ref) + "/" + url.PathEscape(ref.Repo.Owner) + "/" + url.PathEscape(ref.Repo.Name) +
		"/blob/" + commitSHA + "/" + strings.Join(segments, "/") + "?raw=true"
}

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
