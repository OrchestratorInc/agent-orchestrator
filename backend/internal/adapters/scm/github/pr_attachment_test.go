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
	attachRefPath    = "/repos/octocat/hello/git/ref/heads/ao-attachments"
	attachRefsPath   = "/repos/octocat/hello/git/refs"
	attachUpdatePath = "/repos/octocat/hello/git/refs/heads/ao-attachments"
	attachPath       = "pr-42/20261031-094112-a1b2c3-shot.png"
)

func attachmentRequest() ports.SCMAttachmentRequest {
	return ports.SCMAttachmentRequest{PR: validMergeRequest().PR, Name: "shot.png", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nbytes")}
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

func onBlob(t *testing.T, f *fakeGH, want []byte) {
	f.on(http.MethodPost, "/repos/octocat/hello/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Content, Encoding string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil || body.Encoding != "base64" || string(decoded) != string(want) {
			t.Fatalf("blob body = %#v (decoded %q, err %v)", body, decoded, err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob-sha"})
	})
}

func TestUploadAttachment_FirstUploadCreatesOrphanBranchWithReadme(t *testing.T) {
	f := newFakeGH(t)
	req := attachmentRequest()
	onBlob(t, f, req.Data)
	f.on(http.MethodGet, attachRefPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	})
	f.on(http.MethodPost, "/repos/octocat/hello/git/trees", func(w http.ResponseWriter, r *http.Request) {
		var body treeBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.BaseTree != nil || len(body.Tree) != 2 {
			t.Fatalf("orphan tree = %#v", body)
		}
		readme, file := body.Tree[0], body.Tree[1]
		if readme.Path != "README.md" || readme.Mode != "100644" || readme.Type != "blob" || readme.SHA != "" ||
			!strings.Contains(readme.Content, "attached to pull request comments from AO") || !strings.Contains(readme.Content, "Do not merge") {
			t.Fatalf("readme entry = %#v", readme)
		}
		if file != (gitTreeEntry{Path: attachPath, Mode: "100644", Type: "blob", SHA: "blob-sha"}) {
			t.Fatalf("file entry = %#v", file)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree-1"})
	})
	f.on(http.MethodPost, "/repos/octocat/hello/git/commits", func(w http.ResponseWriter, r *http.Request) {
		var body commitBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Message != "Attach shot.png to PR #42" || body.Tree != "tree-1" || body.Parents == nil || len(body.Parents) != 0 {
			t.Fatalf("commit = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit-1"})
	})
	f.on(http.MethodPost, attachRefsPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Ref, SHA string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Ref != "refs/heads/ao-attachments" || body.SHA != "commit-1" {
			t.Fatalf("create ref = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": body.Ref})
	})

	got, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/octocat/hello/blob/ao-attachments/" + attachPath + "?raw=true"
	if got.URL != want || got.Markdown != "" {
		t.Fatalf("result = %#v, want URL %s", got, want)
	}
	if f.callsTo(http.MethodPatch, attachUpdatePath) != 0 {
		t.Fatal("first upload must create the ref, not update it")
	}
}

// onExistingBranch serves an ao-attachments branch whose head is returned by
// heads (called once per ref read) and records each commit's parents.
func onExistingBranch(t *testing.T, f *fakeGH, heads func() string, parents *[]string) {
	f.on(http.MethodGet, attachRefPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Fatalf("ref read must bypass the ETag cache")
		}
		w.Header().Set("ETag", `"same"`)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/ao-attachments", "object": map[string]string{"sha": heads(), "type": "commit"}})
	})
	for _, head := range []string{"head-1", "head-2", "head-3"} {
		head := head
		f.on(http.MethodGet, "/repos/octocat/hello/git/commits/"+head, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": head, "tree": map[string]string{"sha": "tree-of-" + head}})
		})
	}
	f.on(http.MethodPost, "/repos/octocat/hello/git/trees", func(w http.ResponseWriter, r *http.Request) {
		var body treeBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.BaseTree == nil || !strings.HasPrefix(*body.BaseTree, "tree-of-head-") || len(body.Tree) != 1 ||
			body.Tree[0] != (gitTreeEntry{Path: attachPath, Mode: "100644", Type: "blob", SHA: "blob-sha"}) {
			t.Fatalf("append tree = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "new-" + *body.BaseTree})
	})
	f.on(http.MethodPost, "/repos/octocat/hello/git/commits", func(w http.ResponseWriter, r *http.Request) {
		var body commitBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Parents) != 1 || body.Tree != "new-tree-of-"+body.Parents[0] || body.Message != "Attach shot.png to PR #42" {
			t.Fatalf("append commit = %#v", body)
		}
		*parents = append(*parents, body.Parents[0])
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit-on-" + body.Parents[0]})
	})
}

func TestUploadAttachment_LaterUploadAppendsWithBaseTreeAndParent(t *testing.T) {
	f := newFakeGH(t)
	req := attachmentRequest()
	onBlob(t, f, req.Data)
	var parents []string
	onExistingBranch(t, f, func() string { return "head-1" }, &parents)
	f.on(http.MethodPatch, attachUpdatePath, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["sha"] != "commit-on-head-1" || body["force"] != false {
			t.Fatalf("update ref = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "commit-on-head-1"}})
	})
	got, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.URL, "/blob/ao-attachments/"+attachPath+"?raw=true") || len(parents) != 1 || parents[0] != "head-1" {
		t.Fatalf("url = %s parents = %v", got.URL, parents)
	}
	if f.callsTo(http.MethodPost, attachRefsPath) != 0 {
		t.Fatal("existing branch must not be re-created")
	}
}

func TestUploadAttachment_RetriesWhenBranchMovedConcurrently(t *testing.T) {
	f := newFakeGH(t)
	req := attachmentRequest()
	onBlob(t, f, req.Data)
	reads := 0
	var parents []string
	onExistingBranch(t, f, func() string { reads++; return []string{"head-1", "head-2"}[min(reads, 2)-1] }, &parents)
	f.on(http.MethodPatch, attachUpdatePath, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ SHA string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.SHA == "commit-on-head-1" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Update is not a fast forward"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": body.SHA}})
	})
	if _, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), req); err != nil {
		t.Fatal(err)
	}
	if strings.Join(parents, ",") != "head-1,head-2" || f.callsTo(http.MethodPatch, attachUpdatePath) != 2 {
		t.Fatalf("parents = %v, patches = %d", parents, f.callsTo(http.MethodPatch, attachUpdatePath))
	}
	if f.callsTo(http.MethodPost, "/repos/octocat/hello/git/blobs") != 1 {
		t.Fatal("the blob must be uploaded once and reused across retries")
	}
}

func TestUploadAttachment_GivesUpAfterRepeatedRaces(t *testing.T) {
	f := newFakeGH(t)
	req := attachmentRequest()
	onBlob(t, f, req.Data)
	var parents []string
	onExistingBranch(t, f, func() string { return "head-1" }, &parents)
	f.on(http.MethodPatch, attachUpdatePath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Update is not a fast forward"})
	})
	_, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), req)
	if !errors.Is(err, ports.ErrSCMRejected) {
		t.Fatalf("error = %v, want rejected", err)
	}
	if n := f.callsTo(http.MethodPatch, attachUpdatePath); n != attachmentRefAttempts {
		t.Fatalf("patches = %d, want %d", n, attachmentRefAttempts)
	}
}

func TestUploadAttachment_BranchCreatedConcurrentlyFallsBackToAppend(t *testing.T) {
	f := newFakeGH(t)
	req := attachmentRequest()
	onBlob(t, f, req.Data)
	reads := 0
	f.on(http.MethodGet, attachRefPath, func(w http.ResponseWriter, _ *http.Request) {
		reads++
		if reads == 1 {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "head-1", "type": "commit"}})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/git/commits/head-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "tree-of-head-1"}})
	})
	f.on(http.MethodPost, "/repos/octocat/hello/git/trees", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree"})
	})
	f.on(http.MethodPost, "/repos/octocat/hello/git/commits", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit"})
	})
	f.on(http.MethodPost, attachRefsPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Reference already exists"})
	})
	f.on(http.MethodPatch, attachUpdatePath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "commit"}})
	})
	if _, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), req); err != nil {
		t.Fatal(err)
	}
	if f.callsTo(http.MethodPost, attachRefsPath) != 1 || f.callsTo(http.MethodPatch, attachUpdatePath) != 1 {
		t.Fatalf("create=%d update=%d", f.callsTo(http.MethodPost, attachRefsPath), f.callsTo(http.MethodPatch, attachUpdatePath))
	}
}

func TestUploadAttachment_MapsFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, ports.ErrSCMNotFound},
		{http.StatusConflict, ports.ErrSCMRejected}, // empty repository
		{http.StatusUnprocessableEntity, ports.ErrSCMRejected},
	} {
		f := newFakeGH(t)
		f.on(http.MethodPost, "/repos/octocat/hello/git/blobs", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "nope"})
		})
		if _, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), attachmentRequest()); !errors.Is(err, tc.want) {
			t.Errorf("status %d: error = %v, want %v", tc.status, err, tc.want)
		}
	}
	// A rule refusing the branch update is a provider refusal, not a race.
	f := newFakeGH(t)
	req := attachmentRequest()
	onBlob(t, f, req.Data)
	var parents []string
	onExistingBranch(t, f, func() string { return "head-1" }, &parents)
	f.on(http.MethodPatch, attachUpdatePath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Repository rule violations found"})
	})
	if _, err := newAttachmentProvider(t, f).UploadAttachment(ctx(), req); !errors.Is(err, ports.ErrSCMRejected) {
		t.Fatalf("rule violation error = %v", err)
	}
	if n := f.callsTo(http.MethodPatch, attachUpdatePath); n != 1 {
		t.Fatalf("rule violation retried: patches = %d", n)
	}
}

func TestUploadAttachment_RejectsBadInputWithoutCallingGitHub(t *testing.T) {
	f := newFakeGH(t)
	p := newAttachmentProvider(t, f)
	for _, req := range []ports.SCMAttachmentRequest{
		{PR: validMergeRequest().PR, Name: "", Data: []byte("x")},
		{PR: validMergeRequest().PR, Name: "../x.png", Data: []byte("x")},
		{PR: validMergeRequest().PR, Name: "x.png"},
		{PR: ports.SCMPRRef{Repo: validMergeRequest().PR.Repo}, Name: "x.png", Data: []byte("x")},
	} {
		if _, err := p.UploadAttachment(ctx(), req); err == nil {
			t.Errorf("request %#v succeeded", req)
		}
	}
	if len(f.calls()) != 0 {
		t.Fatalf("calls = %d, want none", len(f.calls()))
	}
}

func TestAttachmentPathAndURL(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("PST", -8*3600))
	if got := attachmentPath(7, at, "ff00aa", "a b#?.png"); got != "pr-7/20260102-110405-ff00aa-a b#?.png" {
		t.Fatalf("path = %q", got)
	}
	for _, tc := range []struct {
		name string
		ref  ports.SCMPRRef
		path string
		want string
	}{
		{
			name: "github.com",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "github.com", Owner: "octocat", Name: "hello"}, URL: "https://github.com/octocat/hello/pull/1"},
			path: "pr-1/20260102-110405-ff00aa-shot.png",
			want: "https://github.com/octocat/hello/blob/ao-attachments/pr-1/20260102-110405-ff00aa-shot.png?raw=true",
		},
		{
			name: "segments escaped",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "github.com", Owner: "octocat", Name: "hello"}},
			path: "pr-1/x-a b#?%.png",
			want: "https://github.com/octocat/hello/blob/ao-attachments/pr-1/x-a%20b%23%3F%25.png?raw=true",
		},
		{
			name: "enterprise host",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "Acme.GHE.io", Owner: "team", Name: "app"}},
			path: "pr-3/f.pdf",
			want: "https://acme.ghe.io/team/app/blob/ao-attachments/pr-3/f.pdf?raw=true",
		},
		{
			name: "api host normalized",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Host: "api.github.com", Owner: "o", Name: "r"}},
			path: "pr-1/f.zip",
			want: "https://github.com/o/r/blob/ao-attachments/pr-1/f.zip?raw=true",
		},
		{
			name: "host from PR URL",
			ref:  ports.SCMPRRef{Repo: ports.SCMRepo{Owner: "o", Name: "r"}, URL: "https://www.github.com/o/r/pull/1"},
			path: "pr-1/f.zip",
			want: "https://github.com/o/r/blob/ao-attachments/pr-1/f.zip?raw=true",
		},
	} {
		if got := attachmentURL(tc.ref, tc.path); got != tc.want {
			t.Errorf("%s: url = %s, want %s", tc.name, got, tc.want)
		}
	}
	p := &Provider{}
	if s := p.attachmentSuffix(); len(s) != 6 || strings.Trim(s, "0123456789abcdef") != "" {
		t.Fatalf("random suffix = %q", s)
	}
}
