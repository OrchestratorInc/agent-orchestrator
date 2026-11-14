package pr

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 32)...)
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 32)...)
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	webpBytes = []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x00\x00\x00\x00")
	mp4Bytes  = append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), make([]byte, 16)...)
	movBytes  = append([]byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00qt  "), make([]byte, 16)...)
	webmBytes = []byte("\x1A\x45\xDF\xA3\x9f\x42\x86\x81\x01webm")
	pdfBytes  = []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj\n")
	zipBytes  = append([]byte("PK\x03\x04\x14\x00\x00\x00"), make([]byte, 22)...)
	htmlBytes = []byte("<!DOCTYPE html><html><script>alert(1)</script></html>")
	svgBytes  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
)

func TestSanitizeAttachmentName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"screenshot.png", "screenshot.png"},
		{"Screen Shot 2026-10-31 at 9.41.12 AM.png", "Screen-Shot-2026-10-31-at-9.41.12-AM.png"},
		{"../../etc/passwd.txt", "passwd.txt"},
		{`C:\Users\me\Desktop\report.pdf`, "report.pdf"},
		{"bad\x00na\x1bme\n.log", "badname.log"},
		{"evil\u202egnp.txt", "evilgnp.txt"},
		{".hidden.json", "hidden.json"},
		{"...", "attachment"},
		{".png", "png"},
		{"a  &&  b?.csv", "a-b.csv"},
		{"日本語のファイル.png", "日本語のファイル.png"},
		{strings.Repeat("x", 300) + ".webm", strings.Repeat("x", 95) + ".webm"},
	} {
		if got := sanitizeAttachmentName(tc.in); got != tc.want {
			t.Errorf("sanitizeAttachmentName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateAttachment_AllowedTypes(t *testing.T) {
	for _, tc := range []struct {
		name, declared string
		data           []byte
		wantName       string
		wantType       string
		wantImage      bool
	}{
		{"a.png", "image/png", pngBytes, "a.png", "image/png", true},
		{"a.JPG", "image/jpg", jpegBytes, "a.JPG", "image/jpeg", true},
		{"a.gif", "image/gif", gifBytes, "a.gif", "image/gif", true},
		{"a.webp", "image/webp", webpBytes, "a.webp", "image/webp", true},
		{"clip.mp4", "video/mp4", mp4Bytes, "clip.mp4", "video/mp4", false},
		{"clip.mov", "video/quicktime", movBytes, "clip.mov", "video/quicktime", false},
		{"clip.webm", "video/webm", webmBytes, "clip.webm", "video/webm", false},
		{"doc.pdf", "application/pdf", pdfBytes, "doc.pdf", "application/pdf", false},
		{"run.log", "text/plain; charset=utf-8", []byte("line 1\nline 2\n"), "run.log", "text/plain", false},
		{"data.json", "application/json", []byte(`{"a":1}`), "data.json", "application/json", false},
		{"rows.csv", "text/csv", []byte("a,b\n1,2\n"), "rows.csv", "text/csv", false},
		{"bundle.zip", "application/x-zip-compressed", zipBytes, "bundle.zip", "application/zip", false},
		// No declared type: taken from the extension.
		{"build.log", "", []byte("ok\n"), "build.log", "text/plain", false},
		{"shot.png", "application/octet-stream", pngBytes, "shot.png", "image/png", true},
		// No or foreign extension: the type's own extension is appended.
		{"pasted", "image/png", pngBytes, "pasted.png", "image/png", true},
		{"page.html", "image/png", pngBytes, "page.html.png", "image/png", true},
	} {
		got, err := validateAttachment(tc.name, tc.declared, tc.data)
		if err != nil {
			t.Errorf("%s (%s): %v", tc.name, tc.declared, err)
			continue
		}
		if got.name != tc.wantName || got.contentType != tc.wantType || got.image != tc.wantImage {
			t.Errorf("%s (%s) = %+v, want %s %s image=%v", tc.name, tc.declared, got, tc.wantName, tc.wantType, tc.wantImage)
		}
	}
}

func TestValidateAttachment_Rejections(t *testing.T) {
	for _, tc := range []struct {
		label, name, declared string
		data                  []byte
		want                  error
	}{
		{"blank name", "  ", "image/png", pngBytes, ErrInvalidAttachment},
		{"empty file", "a.png", "image/png", nil, ErrInvalidAttachment},
		{"over 10 MiB", "a.zip", "application/zip", append(append([]byte{}, zipBytes...), make([]byte, MaxAttachmentBytes)...), ErrAttachmentTooLarge},
		{"png renamed .txt", "notes.txt", "text/plain", pngBytes, ErrAttachmentTypeNotAllowed},
		{"png renamed .txt, no declared type", "notes.txt", "", pngBytes, ErrAttachmentTypeNotAllowed},
		{"html as png", "cat.png", "image/png", htmlBytes, ErrAttachmentTypeNotAllowed},
		{"html as text", "cat.txt", "text/plain", htmlBytes, ErrAttachmentTypeNotAllowed},
		{"svg declared", "logo.svg", "image/svg+xml", svgBytes, ErrAttachmentTypeNotAllowed},
		{"svg as png", "logo.png", "image/png", svgBytes, ErrAttachmentTypeNotAllowed},
		{"bare svg markup as text", "logo.txt", "text/plain", []byte(`<svg onload="alert(1)"/>`), ErrAttachmentTypeNotAllowed},
		{"html declared", "page.html", "text/html", htmlBytes, ErrAttachmentTypeNotAllowed},
		{"extension of another type", "clip.mp4", "image/png", pngBytes, ErrAttachmentTypeNotAllowed},
		{"jpeg declared, png bytes", "a.jpg", "image/jpeg", pngBytes, ErrAttachmentTypeNotAllowed},
		{"binary as json", "data.json", "application/json", zipBytes, ErrAttachmentTypeNotAllowed},
		{"unknown type", "a.exe", "application/x-msdownload", []byte("MZ\x90\x00"), ErrAttachmentTypeNotAllowed},
		{"unknown extension, no type", "a.exe", "", []byte("MZ\x90\x00"), ErrAttachmentTypeNotAllowed},
		{"malformed type", "a.png", "image/png;;=", pngBytes, ErrAttachmentTypeNotAllowed},
	} {
		if _, err := validateAttachment(tc.name, tc.declared, tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.label, err, tc.want)
		}
	}
	// Exactly 10 MiB is allowed.
	exact := append(append([]byte{}, zipBytes...), make([]byte, MaxAttachmentBytes-len(zipBytes))...)
	if _, err := validateAttachment("a.zip", "application/zip", exact); err != nil {
		t.Fatalf("10 MiB attachment: %v", err)
	}
}

// fakeAttachmentStore records what a comment's files did with the provider.
type fakeAttachmentStore struct {
	access     ports.SCMAttachmentAccess
	accessErr  error
	stored     ports.SCMStoredAttachments
	storeErr   error
	batches    []ports.SCMAttachmentBatch
	discarded  []string
	accessRefs []ports.SCMPRRef
}

func (f *fakeAttachmentStore) AttachmentAccess(_ context.Context, pr ports.SCMPRRef) (ports.SCMAttachmentAccess, error) {
	f.accessRefs = append(f.accessRefs, pr)
	return f.access, f.accessErr
}

func (f *fakeAttachmentStore) StoreAttachments(_ context.Context, batch ports.SCMAttachmentBatch) (ports.SCMStoredAttachments, error) {
	f.batches = append(f.batches, batch)
	return f.stored, f.storeErr
}

func (f *fakeAttachmentStore) DiscardAttachments(_ context.Context, _ ports.SCMPRRef, handle string) error {
	f.discarded = append(f.discarded, handle)
	return nil
}

func allowedStore() *fakeAttachmentStore {
	return &fakeAttachmentStore{
		access: ports.SCMAttachmentAccess{Allowed: true, Location: "acme/widgets"},
		stored: ports.SCMStoredAttachments{
			URLs:   []string{"https://github.com/acme/widgets/blob/c1/My-Shot.png?raw=true", "https://github.com/acme/widgets/blob/c1/run.log?raw=true"},
			Handle: "refs/ao/attachments/pr-42/x",
		},
	}
}

func TestPostComment_StoresItsFilesWhenPostingAndLinksThem(t *testing.T) {
	pr := openPRFixture()
	scm := &fakeStateCommentSCM{commentResult: ports.SCMCommentResult{ID: "IC_1"}}
	files := allowedStore()
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: files})
	_, err := svc.PostComment(context.Background(), CommentRequest{
		PRID: "42", PRURL: pr.URL,
		Body: "See ![My Shot](attachment:0) and [log](attachment:1), again ![](attachment:0)",
		Attachments: []CommentAttachment{
			{Name: "../My Shot.png", ContentType: "image/png", Data: pngBytes},
			{Name: "run.log", Data: []byte("ok\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.batches) != 1 {
		t.Fatalf("batches = %d", len(files.batches))
	}
	batch := files.batches[0]
	if len(batch.Files) != 2 || batch.Files[0].Name != "My-Shot.png" || batch.Files[0].ContentType != "image/png" || !bytes.Equal(batch.Files[0].Data, pngBytes) ||
		batch.PR.Number != 42 || batch.PR.Repo.Owner != "acme" || batch.PR.Repo.Name != "widgets" {
		t.Fatalf("batch = %#v", batch)
	}
	want := "See ![My Shot](" + files.stored.URLs[0] + ") and [log](" + files.stored.URLs[1] + "), again ![](" + files.stored.URLs[0] + ")"
	if len(scm.commentRequests) != 1 || scm.commentRequests[0].Body != want {
		t.Fatalf("posted body = %q", scm.commentRequests[0].Body)
	}
	if len(files.discarded) != 0 {
		t.Fatalf("discarded = %q", files.discarded)
	}
}

func TestPostComment_RemovesItsFilesWhenThePostFails(t *testing.T) {
	pr := openPRFixture()
	scm := &fakeStateCommentSCM{commentErr: ports.ErrSCMRejected}
	files := allowedStore()
	files.stored.URLs = files.stored.URLs[:1]
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: files})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the request is gone; the files still go
	_, err := svc.PostComment(ctx, CommentRequest{
		PRID: "42", PRURL: pr.URL, Body: "![x](attachment:0)",
		Attachments: []CommentAttachment{{Name: "a.png", ContentType: "image/png", Data: pngBytes}},
	})
	if !errors.Is(err, ErrPRRejected) {
		t.Fatalf("error = %v", err)
	}
	if len(files.discarded) != 1 || files.discarded[0] != "refs/ao/attachments/pr-42/x" {
		t.Fatalf("discarded = %q", files.discarded)
	}
}

func TestPostComment_WithoutWriteAccessStoresNothing(t *testing.T) {
	pr := openPRFixture()
	scm := &fakeStateCommentSCM{}
	files := &fakeAttachmentStore{access: ports.SCMAttachmentAccess{Location: "acme/widgets", Reason: ports.SCMAttachmentNoWriteAccess}}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: files})
	_, err := svc.PostComment(context.Background(), CommentRequest{
		PRID: "42", PRURL: pr.URL, Body: "![x](attachment:0)",
		Attachments: []CommentAttachment{{Name: "a.png", ContentType: "image/png", Data: pngBytes}},
	})
	if !errors.Is(err, ErrAttachmentsForbidden) {
		t.Fatalf("error = %v", err)
	}
	if len(files.batches) != 0 || len(scm.commentRequests) != 0 {
		t.Fatalf("stored %d batches, posted %d comments", len(files.batches), len(scm.commentRequests))
	}
	// A provider that refuses the write mid-way reads the same.
	files = allowedStore()
	files.storeErr = ports.ErrSCMForbidden
	svc = NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: files})
	if _, err := svc.PostComment(context.Background(), CommentRequest{
		PRID: "42", PRURL: pr.URL, Body: "![x](attachment:0)",
		Attachments: []CommentAttachment{{Name: "a.png", ContentType: "image/png", Data: pngBytes}},
	}); !errors.Is(err, ErrAttachmentsForbidden) {
		t.Fatalf("store forbidden error = %v", err)
	}
}

func TestPostComment_FilesMustMatchTheBodysLinks(t *testing.T) {
	pr := openPRFixture()
	png := CommentAttachment{Name: "a.png", ContentType: "image/png", Data: pngBytes}
	big := make([]CommentAttachment, 3)
	for i := range big {
		big[i] = CommentAttachment{Name: "a.zip", ContentType: "application/zip", Data: append(append([]byte{}, zipBytes...), make([]byte, MaxAttachmentBytes-len(zipBytes))...)}
	}
	eleven := make([]CommentAttachment, 11)
	for i := range eleven {
		eleven[i] = png
	}
	for _, tc := range []struct {
		name  string
		body  string
		files []CommentAttachment
		want  error
	}{
		{name: "a link to no file", body: "![x](attachment:1)", files: []CommentAttachment{png}, want: ErrInvalidAttachment},
		{name: "a file never linked", body: "no links", files: []CommentAttachment{png}, want: ErrInvalidAttachment},
		{name: "too many files", body: "![x](attachment:0)", files: eleven, want: ErrInvalidAttachment},
		{name: "too large together", body: "[a](attachment:0)[b](attachment:1)[c](attachment:2)", files: big, want: ErrAttachmentTooLarge},
		{name: "renamed file", body: "[x](attachment:0)", files: []CommentAttachment{{Name: "a.txt", ContentType: "text/plain", Data: pngBytes}}, want: ErrAttachmentTypeNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scm := &fakeStateCommentSCM{}
			files := allowedStore()
			svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: files})
			if _, err := svc.PostComment(context.Background(), CommentRequest{PRID: "42", PRURL: pr.URL, Body: tc.body, Attachments: tc.files}); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if len(files.batches) != 0 || len(scm.commentRequests) != 0 {
				t.Fatalf("stored %d batches, posted %d comments", len(files.batches), len(scm.commentRequests))
			}
		})
	}
}

func TestPostComment_LeavesAttachmentLinksInCodeAlone(t *testing.T) {
	pr := openPRFixture()
	// With no files, a comment that shows the syntax posts as written.
	scm := &fakeStateCommentSCM{commentResult: ports.SCMCommentResult{ID: "IC_1"}}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: allowedStore()})
	plain := "Link a file with `![shot.png](attachment:0)`."
	if _, err := svc.PostComment(context.Background(), CommentRequest{PRID: "42", PRURL: pr.URL, Body: plain}); err != nil {
		t.Fatal(err)
	}
	if scm.commentRequests[0].Body != plain {
		t.Fatalf("posted body = %q", scm.commentRequests[0].Body)
	}
	// With files, only links outside code are files; the examples stay as text.
	scm = &fakeStateCommentSCM{commentResult: ports.SCMCommentResult{ID: "IC_2"}}
	files := allowedStore()
	files.stored.URLs = files.stored.URLs[:1]
	svc = NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, Attachments: files})
	body := "![shot](attachment:0)\n\n```md\n![example](attachment:3)\n```\nand `[x](attachment:0)` and ``[y](attachment:2)``"
	if _, err := svc.PostComment(context.Background(), CommentRequest{
		PRID: "42", PRURL: pr.URL, Body: body,
		Attachments: []CommentAttachment{{Name: "shot.png", ContentType: "image/png", Data: pngBytes}},
	}); err != nil {
		t.Fatal(err)
	}
	want := "![shot](" + files.stored.URLs[0] + ")\n\n```md\n![example](attachment:3)\n```\nand `[x](attachment:0)` and ``[y](attachment:2)``"
	if scm.commentRequests[0].Body != want {
		t.Fatalf("posted body = %q", scm.commentRequests[0].Body)
	}
}

func TestAttachmentAccess(t *testing.T) {
	pr := openPRFixture()
	files := allowedStore()
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Attachments: files})
	got, err := svc.AttachmentAccess(context.Background(), "42", pr.URL)
	if err != nil || !got.Allowed || got.Location != "acme/widgets" {
		t.Fatalf("access = %#v, err %v", got, err)
	}
	if len(files.accessRefs) != 1 || files.accessRefs[0].Number != 42 {
		t.Fatalf("access refs = %#v", files.accessRefs)
	}
	if _, err := svc.AttachmentAccess(context.Background(), "7", pr.URL); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("mismatched number error = %v", err)
	}
	// A provider that can't store files reads as not allowed, not as a failure.
	files.accessErr = ports.ErrSCMUnsupported
	if got, err := svc.AttachmentAccess(context.Background(), "42", pr.URL); err != nil || got.Allowed || got.Reason != "unsupported" {
		t.Fatalf("unsupported access = %#v, err %v", got, err)
	}
	none := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}})
	if got, err := none.AttachmentAccess(context.Background(), "42", pr.URL); err != nil || got.Allowed || got.Reason != "unsupported" {
		t.Fatalf("no store access = %#v, err %v", got, err)
	}
}
