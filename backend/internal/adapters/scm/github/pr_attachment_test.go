package github

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	attachRepoPath    = "/repos/octocat/hello"
	attachBlobsPath   = "/repos/octocat/hello/git/blobs"
	attachTreesPath   = "/repos/octocat/hello/git/trees"
	attachCommitsPath = "/repos/octocat/hello/git/commits"
	attachRefsPath    = "/repos/octocat/hello/git/refs"
	attachRef         = "refs/ao/attachments/pr-42/20261031-094112-a1b2c3"
	attachRefPath     = "/repos/octocat/hello/git/refs/ao/attachments/pr-42/20261031-094112-a1b2c3"
)

func attachmentBatch(files ...ports.SCMAttachmentFile) ports.SCMAttachmentBatch {
	return ports.SCMAttachmentBatch{PR: validMergeRequest().PR, Files: files}
}

func pngFile(name string) ports.SCMAttachmentFile {
	return ports.SCMAttachmentFile{Name: name, ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n" + name)}
}

func newAttachmentProvider(t *testing.T, f *fakeGH) *Provider {
	t.Helper()
	p := newProviderForTest(t, f)
	p.now = func() time.Time { return time.Date(2026, 10, 31, 15, 11, 12, 0, time.FixedZone("IST", 5*3600+1800)) }
	p.randomSuffix = func() string { return "a1b2c3" }
	return p
}

type treeBody struct {
	BaseTree *string        `json:"base_tree"`
	Tree     []gitTreeEntry `json:"tree"`
}

type commitBody struct {
	Message string   `json:"message"`
	Tree    string   `json:"tree"`
	Parents []string `json:"parents"`
}

// onGitData answers the blob, tree, commit and ref calls of one stored batch,
// recording what was sent: each blob gets "blob-<n>", the tree "tree-sha" and
// the commit "commit-sha".
func onGitData(t *testing.T, f *fakeGH) (*[][]byte, *treeBody, *commitBody, *map[string]string) {
	t.Helper()
	var blobs [][]byte
	var tree treeBody
	var commit commitBody
	var ref map[string]string
	f.on(http.MethodPost, attachBlobsPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Content, Encoding string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Encoding != "base64" {
			t.Fatalf("blob body = %#v, err %v", body, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			t.Fatal(err)
		}
		blobs = append(blobs, decoded)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob-" + string(rune('0'+len(blobs)))})
	})
	f.on(http.MethodPost, attachTreesPath, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&tree); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree-sha"})
	})
	f.on(http.MethodPost, attachCommitsPath, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&commit); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit-sha"})
	})
	f.on(http.MethodPost, attachRefsPath, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&ref); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": ref["ref"], "object": map[string]string{"sha": ref["sha"]}})
	})
	return &blobs, &tree, &commit, &ref
}

func TestStoreAttachments_CommitsTheCommentsFilesUnderTheirOwnHiddenRef(t *testing.T) {
	f := newFakeGH(t)
	blobs, tree, commit, ref := onGitData(t, f)
	batch := attachmentBatch(pngFile("shot.png"), pngFile("shot.png"), pngFile("a b#?.png"))

	stored, err := newAttachmentProvider(t, f).StoreAttachments(ctx(), batch)
	if err != nil {
		t.Fatal(err)
	}

	if len(*blobs) != 3 || string((*blobs)[2]) != string(batch.Files[2].Data) {
		t.Fatalf("blobs = %q", *blobs)
	}
	// One tree, with no base: the commit holds only this comment's files, and a
	// repeated name keeps its own path.
	if tree.BaseTree != nil {
		t.Fatalf("tree has a base: %q", *tree.BaseTree)
	}
	gotPaths := []string{}
	for _, entry := range tree.Tree {
		gotPaths = append(gotPaths, entry.Path)
		if entry.Mode != "100644" || entry.Type != "blob" || !strings.HasPrefix(entry.SHA, "blob-") {
			t.Fatalf("tree entry = %#v", entry)
		}
	}
	if strings.Join(gotPaths, "|") != "shot.png|2-shot.png|a b#?.png" {
		t.Fatalf("tree paths = %q", gotPaths)
	}
	// An orphan commit that CI systems skip.
	if len(commit.Parents) != 0 || commit.Tree != "tree-sha" || !strings.Contains(commit.Message, "[skip ci]") || !strings.Contains(commit.Message, "PR #42") {
		t.Fatalf("commit = %#v", *commit)
	}
	// Under the hidden namespace, never refs/heads.
	if (*ref)["ref"] != attachRef || (*ref)["sha"] != "commit-sha" {
		t.Fatalf("ref = %#v", *ref)
	}
	if stored.Handle != attachRef {
		t.Fatalf("handle = %q", stored.Handle)
	}
	wantURLs := []string{
		"https://github.com/octocat/hello/blob/commit-sha/shot.png?raw=true",
		"https://github.com/octocat/hello/blob/commit-sha/2-shot.png?raw=true",
		"https://github.com/octocat/hello/blob/commit-sha/a%20b%23%3F.png?raw=true",
	}
	if strings.Join(stored.URLs, " ") != strings.Join(wantURLs, " ") {
		t.Fatalf("urls = %q", stored.URLs)
	}
}

func TestStoreAttachments_NoWriteAccessIsForbiddenAndARuleIsARejection(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, ports.ErrSCMForbidden},
		{http.StatusForbidden, ports.ErrSCMForbidden},
		{http.StatusConflict, ports.ErrSCMRejected}, // empty repository
		{http.StatusUnprocessableEntity, ports.ErrSCMRejected},
	} {
		f := newFakeGH(t)
		f.on(http.MethodPost, attachBlobsPath, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "nope"})
		})
		if _, err := newAttachmentProvider(t, f).StoreAttachments(ctx(), attachmentBatch(pngFile("shot.png"))); !errors.Is(err, tc.want) {
			t.Errorf("status %d: error = %v, want %v", tc.status, err, tc.want)
		}
	}
	// A repository rule refusing the ref is a rejection too.
	f := newFakeGH(t)
	onGitData(t, f)
	f.on(http.MethodPost, attachRefsPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Repository rule violations found"})
	})
	if _, err := newAttachmentProvider(t, f).StoreAttachments(ctx(), attachmentBatch(pngFile("shot.png"))); !errors.Is(err, ports.ErrSCMRejected) {
		t.Fatalf("rule violation error = %v", err)
	}
}

func TestStoreAttachments_RejectsBadInputWithoutCallingGitHub(t *testing.T) {
	f := newFakeGH(t)
	p := newAttachmentProvider(t, f)
	for _, batch := range []ports.SCMAttachmentBatch{
		attachmentBatch(),
		attachmentBatch(ports.SCMAttachmentFile{Name: "", Data: []byte("x")}),
		attachmentBatch(ports.SCMAttachmentFile{Name: "../x.png", Data: []byte("x")}),
		attachmentBatch(ports.SCMAttachmentFile{Name: "x.png"}),
		{PR: ports.SCMPRRef{Repo: validMergeRequest().PR.Repo}, Files: []ports.SCMAttachmentFile{pngFile("x.png")}},
	} {
		if _, err := p.StoreAttachments(ctx(), batch); err == nil {
			t.Errorf("batch %#v succeeded", batch)
		}
	}
	if len(f.calls()) != 0 {
		t.Fatalf("calls = %d, want none", len(f.calls()))
	}
}

func TestDiscardAttachments_DeletesOnlyAnAttachmentsRef(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodDelete, attachRefPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	p := newAttachmentProvider(t, f)
	pr := validMergeRequest().PR
	if err := p.DiscardAttachments(ctx(), pr, attachRef); err != nil {
		t.Fatal(err)
	}
	if n := f.callsTo(http.MethodDelete, attachRefPath); n != 1 {
		t.Fatalf("deletes = %d", n)
	}
	// Anything outside the namespace is never deleted.
	for _, handle := range []string{"refs/heads/main", "refs/ao/attachments/", "refs/ao/attachments/../../heads/main", ""} {
		if err := p.DiscardAttachments(ctx(), pr, handle); err == nil {
			t.Errorf("handle %q accepted", handle)
		}
	}
	if len(f.calls()) != 1 {
		t.Fatalf("calls = %d, want only the one delete", len(f.calls()))
	}
}

func TestDiscardAttachments_AnAlreadyDeletedRefIsFine(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodDelete, attachRefPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Reference does not exist"})
	})
	if err := newAttachmentProvider(t, f).DiscardAttachments(ctx(), validMergeRequest().PR, attachRef); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentAccess_FollowsTheAccountsPushPermission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		push   bool
		want   ports.SCMAttachmentAccess
	}{
		{name: "writer", status: http.StatusOK, push: true, want: ports.SCMAttachmentAccess{Allowed: true, Location: "octocat/hello"}},
		{name: "reader", status: http.StatusOK, want: ports.SCMAttachmentAccess{Location: "octocat/hello", Reason: ports.SCMAttachmentNoWriteAccess}},
		{name: "hidden", status: http.StatusNotFound, want: ports.SCMAttachmentAccess{Location: "octocat/hello", Reason: ports.SCMAttachmentNoWriteAccess}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodGet, attachRepoPath, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "octocat/hello", "permissions": map[string]bool{"pull": true, "push": tc.push}})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				}
			})
			got, err := newAttachmentProvider(t, f).AttachmentAccess(ctx(), validMergeRequest().PR)
			if err != nil || got != tc.want {
				t.Fatalf("access = %#v, err %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestAttachmentURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  ports.SCMPRRef
		path string
		want string
	}{
		{
			name: "github.com",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "github.com", Owner: "octocat", Name: "hello"}, URL: "https://github.com/octocat/hello/pull/1"},
			path: "shot.png",
			want: "https://github.com/octocat/hello/blob/c1/shot.png?raw=true",
		},
		{
			name: "name escaped",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "github.com", Owner: "octocat", Name: "hello"}},
			path: "a b#?%.png",
			want: "https://github.com/octocat/hello/blob/c1/a%20b%23%3F%25.png?raw=true",
		},
		{
			name: "enterprise host",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "Acme.GHE.io", Owner: "team", Name: "app"}},
			path: "f.pdf",
			want: "https://acme.ghe.io/team/app/blob/c1/f.pdf?raw=true",
		},
		{
			name: "api host normalized",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "api.github.com", Owner: "o", Name: "r"}},
			path: "f.zip",
			want: "https://github.com/o/r/blob/c1/f.zip?raw=true",
		},
		{
			name: "host from PR URL",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Owner: "o", Name: "r"}, URL: "https://www.github.com/o/r/pull/1"},
			path: "f.zip",
			want: "https://github.com/o/r/blob/c1/f.zip?raw=true",
		},
	} {
		if got := attachmentURL(tc.ref, "c1", tc.path); got != tc.want {
			t.Errorf("%s: url = %s, want %s", tc.name, got, tc.want)
		}
	}
	p := &Provider{}
	if s := p.attachmentSuffix(); len(s) != 6 || strings.Trim(s, "0123456789abcdef") != "" {
		t.Fatalf("random suffix = %q", s)
	}
}
