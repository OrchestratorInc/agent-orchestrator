package gitlab

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestUploadAttachment_GitLabUsesProjectUploadsMultipart(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nbytes")
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v4/projects/myorg%2Fmyrepo/uploads" {
			t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(part)
		if part.FormName() != "file" || part.FileName() != "shot.png" || part.Header.Get("Content-Type") != "image/png" || string(body) != string(data) {
			t.Fatalf("part name=%q file=%q type=%q body=%q", part.FormName(), part.FileName(), part.Header.Get("Content-Type"), body)
		}
		if _, err := reader.NextPart(); err != io.EOF {
			t.Fatalf("extra form parts: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 5, "alt": "shot", "url": "/uploads/abc123/shot.png",
			"full_path": "/-/project/77/uploads/abc123/shot.png",
			"markdown":  "![shot](/uploads/abc123/shot.png)",
		})
	}))
	got, err := p.UploadAttachment(ctx(), ports.SCMAttachmentRequest{PR: validMergeRequest().PR, Name: "shot.png", ContentType: "image/png", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://gitlab.com/-/project/77/uploads/abc123/shot.png" || got.Markdown != "![shot](/uploads/abc123/shot.png)" {
		t.Fatalf("result = %#v", got)
	}
}

func TestUploadAttachment_GitLabURLFallbacksAndFailures(t *testing.T) {
	ref := validMergeRequest().PR
	if got := uploadURL(ref, "/uploads/s/f.log", ""); got != "https://gitlab.com/myorg/myrepo/uploads/s/f.log" {
		t.Fatalf("relative url = %s", got)
	}
	selfManaged := ports.SCMPRRef{Repo: ports.SCMRepo{Host: "git.example.com", Owner: "g/sub", Name: "p"}, URL: "http://git.example.com/g/sub/p/-/merge_requests/1"}
	if got := uploadURL(selfManaged, "/uploads/s/f.log", "/g/sub/p/uploads/s/f.log"); got != "http://git.example.com/g/sub/p/uploads/s/f.log" {
		t.Fatalf("self-managed url = %s", got)
	}

	for _, tc := range []struct {
		status int
		want   error
	}{{http.StatusNotFound, ports.ErrSCMNotFound}, {http.StatusBadRequest, ports.ErrSCMRejected}, {http.StatusRequestEntityTooLarge, nil}} {
		_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "nope"})
		}))
		_, err := p.UploadAttachment(ctx(), ports.SCMAttachmentRequest{PR: ref, Name: "f.log", ContentType: "text/plain", Data: []byte("x")})
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("status %d: error = %v, want %v", tc.status, err, tc.want)
		}
	}
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":1}`)
	}))
	if _, err := p.UploadAttachment(ctx(), ports.SCMAttachmentRequest{PR: ref, Name: "f.log", Data: []byte("x")}); err == nil || !strings.Contains(err.Error(), "missing url") {
		t.Fatalf("missing url error = %v", err)
	}
}
