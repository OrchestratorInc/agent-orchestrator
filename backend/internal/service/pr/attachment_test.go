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

type fakeUploader struct {
	result   ports.SCMAttachmentResult
	err      error
	requests []ports.SCMAttachmentRequest
}

func (f *fakeUploader) UploadAttachment(_ context.Context, request ports.SCMAttachmentRequest) (ports.SCMAttachmentResult, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func TestActionServiceUploadAttachment_SendsSanitizedFileAndBuildsMarkdown(t *testing.T) {
	pr := openPRFixture()
	up := &fakeUploader{result: ports.SCMAttachmentResult{URL: "https://github.com/acme/widgets/blob/ao-attachments/pr-42/f.png?raw=true"}}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Uploader: up})
	result, err := svc.UploadAttachment(context.Background(), AttachmentRequest{PRID: "42", PRURL: pr.URL, Name: "../My Shot.png", ContentType: "image/png", Data: pngBytes})
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "My-Shot.png" || result.URL != up.result.URL || result.Markdown != "![My-Shot.png]("+up.result.URL+")" {
		t.Fatalf("result = %#v", result)
	}
	if len(up.requests) != 1 {
		t.Fatalf("requests = %d", len(up.requests))
	}
	got := up.requests[0]
	if got.Name != "My-Shot.png" || got.ContentType != "image/png" || !bytes.Equal(got.Data, pngBytes) ||
		got.PR.Number != 42 || got.PR.URL != pr.URL || got.PR.Repo.Provider != "github" || got.PR.Repo.Owner != "acme" || got.PR.Repo.Name != "widgets" || got.PR.Repo.Host != "github.com" {
		t.Fatalf("request = %#v", got)
	}

	// Non-images are linked, and a provider's own markdown wins.
	result, err = svc.UploadAttachment(context.Background(), AttachmentRequest{PRID: "42", PRURL: pr.URL, Name: "run.log", Data: []byte("ok\n")})
	if err != nil || result.Markdown != "[run.log]("+up.result.URL+")" {
		t.Fatalf("log markdown = %q, err %v", result.Markdown, err)
	}
	up.result.Markdown = "![run](/uploads/abc/run.log)"
	result, err = svc.UploadAttachment(context.Background(), AttachmentRequest{PRID: "42", PRURL: pr.URL, Name: "run.log", Data: []byte("ok\n")})
	if err != nil || result.Markdown != up.result.Markdown {
		t.Fatalf("provider markdown = %q, err %v", result.Markdown, err)
	}
}

func TestActionServiceUploadAttachment_ValidationAndErrorMapping(t *testing.T) {
	pr := openPRFixture()
	ok := AttachmentRequest{PRID: "42", PRURL: pr.URL, Name: "a.png", ContentType: "image/png", Data: pngBytes}
	for _, tc := range []struct {
		name    string
		store   *fakeActionStore
		request AttachmentRequest
		scmErr  error
		want    error
		calls   int
	}{
		{name: "bad id", store: &fakeActionStore{pr: pr, ok: true}, request: AttachmentRequest{PRID: "x", PRURL: pr.URL, Name: "a.png", Data: pngBytes}, want: ErrInvalidPR},
		{name: "renamed file", store: &fakeActionStore{pr: pr, ok: true}, request: AttachmentRequest{PRID: "42", PRURL: pr.URL, Name: "a.txt", ContentType: "text/plain", Data: pngBytes}, want: ErrAttachmentTypeNotAllowed},
		{name: "unknown pr", store: &fakeActionStore{}, request: ok, want: ErrPRNotFound},
		{name: "number mismatch", store: &fakeActionStore{pr: pr, ok: true}, request: AttachmentRequest{PRID: "7", PRURL: pr.URL, Name: "a.png", Data: pngBytes}, want: ErrPRNotFound},
		{name: "provider not found", store: &fakeActionStore{pr: pr, ok: true}, request: ok, scmErr: ports.ErrSCMNotFound, want: ErrPRNotFound, calls: 1},
		{name: "provider refused", store: &fakeActionStore{pr: pr, ok: true}, request: ok, scmErr: ports.ErrSCMRejected, want: ErrPRRejected, calls: 1},
		{name: "unsupported", store: &fakeActionStore{pr: pr, ok: true}, request: ok, scmErr: ports.ErrSCMUnsupported, want: ErrUnsupported, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeUploader{err: tc.scmErr, result: ports.SCMAttachmentResult{URL: "u"}}
			svc := NewActionService(ActionDeps{Store: tc.store, Uploader: up})
			if _, err := svc.UploadAttachment(context.Background(), tc.request); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if len(up.requests) != tc.calls {
				t.Fatalf("provider calls = %d, want %d", len(up.requests), tc.calls)
			}
		})
	}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}})
	if _, err := svc.UploadAttachment(context.Background(), ok); err == nil {
		t.Fatal("upload without an uploader succeeded")
	}
}
