package pr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// MaxAttachmentBytes caps one decoded comment attachment.
const MaxAttachmentBytes = 10 << 20

// maxAttachmentNameBytes bounds the sanitized file name so the stored path
// stays readable; the extension is always kept.
const maxAttachmentNameBytes = 100

// attachmentType is one allowed attachment media type: the file extensions
// that may carry it and how its bytes are recognized.
type attachmentType struct {
	extensions []string
	image      bool
	// sniff reports whether the file content is this type.
	sniff func(data []byte, sniffed string) bool
}

func sniffedAs(want string) func([]byte, string) bool {
	return func(_ []byte, sniffed string) bool { return sniffed == want }
}

// sniffISOMedia accepts content http.DetectContentType reports as MP4, plus
// ISO base media / QuickTime files it does not (a .mov with only "qt  "
// brands, or an older QuickTime file that starts with a moov/mdat atom).
func sniffISOMedia(data []byte, sniffed string) bool {
	if sniffed == "video/mp4" {
		return true
	}
	if sniffed != "application/octet-stream" || len(data) < 8 {
		return false
	}
	switch string(data[4:8]) {
	case "ftyp", "moov", "mdat", "wide", "free", "skip", "pnot":
		return true
	default:
		return false
	}
}

// sniffText accepts plain text that does not open like markup a browser
// could render.
func sniffText(data []byte, sniffed string) bool {
	if sniffed != "text/plain" {
		return false
	}
	head := bytes.ToLower(bytes.TrimLeftFunc(data[:min(len(data), 512)], unicode.IsSpace))
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	for _, markup := range []string{"<svg", "<html", "<!doctype", "<?xml", "<script"} {
		if bytes.HasPrefix(head, []byte(markup)) {
			return false
		}
	}
	return true
}

// attachmentTypes is the allowlist. SVG and HTML are deliberately absent: both
// can carry script.
var attachmentTypes = map[string]attachmentType{
	"image/png":        {extensions: []string{".png"}, image: true, sniff: sniffedAs("image/png")},
	"image/jpeg":       {extensions: []string{".jpg", ".jpeg"}, image: true, sniff: sniffedAs("image/jpeg")},
	"image/gif":        {extensions: []string{".gif"}, image: true, sniff: sniffedAs("image/gif")},
	"image/webp":       {extensions: []string{".webp"}, image: true, sniff: sniffedAs("image/webp")},
	"video/mp4":        {extensions: []string{".mp4", ".m4v"}, sniff: sniffISOMedia},
	"video/quicktime":  {extensions: []string{".mov", ".qt"}, sniff: sniffISOMedia},
	"video/webm":       {extensions: []string{".webm"}, sniff: sniffedAs("video/webm")},
	"application/pdf":  {extensions: []string{".pdf"}, sniff: sniffedAs("application/pdf")},
	"text/plain":       {extensions: []string{".txt", ".log", ".text"}, sniff: sniffText},
	"application/json": {extensions: []string{".json"}, sniff: sniffText},
	"text/csv":         {extensions: []string{".csv"}, sniff: sniffText},
	"application/zip":  {extensions: []string{".zip"}, sniff: sniffedAs("application/zip")},
}

// attachmentTypeAliases maps other names clients use for an allowed type.
var attachmentTypeAliases = map[string]string{
	"image/jpg":                    "image/jpeg",
	"image/pjpeg":                  "image/jpeg",
	"video/mov":                    "video/quicktime",
	"video/x-quicktime":            "video/quicktime",
	"video/x-m4v":                  "video/mp4",
	"text/x-log":                   "text/plain",
	"text/json":                    "application/json",
	"application/csv":              "text/csv",
	"text/comma-separated-values":  "text/csv",
	"application/x-zip-compressed": "application/zip",
	"application/x-zip":            "application/zip",
}

// validatedAttachment is an attachment that passed every check.
type validatedAttachment struct {
	name        string
	contentType string
	image       bool
}

// validateAttachment sanitizes the name and checks size and type. The
// declared type must be allowed and the sniffed content must match it, and an
// extension that belongs to a different allowed type is refused, so a renamed
// file cannot pass as something else. A name without the type's extension
// gets it appended. An empty or generic declared type is taken from the
// extension.
func validateAttachment(name, declared string, data []byte) (validatedAttachment, error) {
	if strings.TrimSpace(name) == "" {
		return validatedAttachment{}, fmt.Errorf("%w: name is required", ErrInvalidAttachment)
	}
	if len(data) == 0 {
		return validatedAttachment{}, fmt.Errorf("%w: file is empty", ErrInvalidAttachment)
	}
	if len(data) > MaxAttachmentBytes {
		return validatedAttachment{}, fmt.Errorf("%w: %d bytes exceeds %d", ErrAttachmentTooLarge, len(data), MaxAttachmentBytes)
	}
	safe := sanitizeAttachmentName(name)
	ext := strings.ToLower(path.Ext(safe))

	contentType, err := canonicalAttachmentType(declared)
	if err != nil {
		return validatedAttachment{}, err
	}
	if contentType == "" {
		if contentType = attachmentTypeForExtension(ext); contentType == "" {
			return validatedAttachment{}, fmt.Errorf("%w: cannot tell the type of %q", ErrAttachmentTypeNotAllowed, safe)
		}
	}
	allowed, ok := attachmentTypes[contentType]
	if !ok {
		return validatedAttachment{}, fmt.Errorf("%w: %s", ErrAttachmentTypeNotAllowed, contentType)
	}
	if !containsString(allowed.extensions, ext) {
		// An extension that belongs to another allowed type means the file was
		// renamed or mislabeled. Any other suffix ("notes.v2", "page.html") is
		// kept but followed by the type's own extension.
		if other := attachmentTypeForExtension(ext); other != "" {
			return validatedAttachment{}, fmt.Errorf("%w: extension %s does not match %s", ErrAttachmentTypeNotAllowed, ext, contentType)
		}
		safe = appendExtension(safe, allowed.extensions[0])
	}
	sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	if !allowed.sniff(data, sniffed) {
		return validatedAttachment{}, fmt.Errorf("%w: content is %s, not %s", ErrAttachmentTypeNotAllowed, sniffed, contentType)
	}
	return validatedAttachment{name: safe, contentType: contentType, image: allowed.image}, nil
}

// canonicalAttachmentType normalizes a declared media type. It returns "" for
// an absent or generic type so the caller falls back to the extension.
func canonicalAttachmentType(declared string) (string, error) {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return "", nil
	}
	mediaType, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return "", fmt.Errorf("%w: malformed content type %q", ErrAttachmentTypeNotAllowed, declared)
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "application/octet-stream" {
		return "", nil
	}
	if alias, ok := attachmentTypeAliases[mediaType]; ok {
		mediaType = alias
	}
	return mediaType, nil
}

func attachmentTypeForExtension(ext string) string {
	if ext == "" {
		return ""
	}
	for contentType, allowed := range attachmentTypes {
		if containsString(allowed.extensions, ext) {
			return contentType
		}
	}
	return ""
}

// sanitizeAttachmentName reduces a client-supplied file name to a safe one:
// only the last path segment is kept, control characters are dropped, and
// anything but letters, digits, '.', '-', and '_' becomes '-'. Leading dots
// and dashes are trimmed so the result is never hidden or "..". The extension
// survives truncation, and an empty stem becomes "attachment".
func sanitizeAttachmentName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r == utf8.RuneError || unicode.IsControl(r) || unicode.In(r, unicode.Cf):
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	safe := strings.TrimLeft(b.String(), ".-")
	safe = strings.TrimRight(safe, ".-")
	ext := path.Ext(safe)
	if len(ext) > 16 || ext == "." {
		ext = ""
	}
	stem := strings.TrimRight(strings.TrimSuffix(safe, ext), ".-")
	if limit := maxAttachmentNameBytes - len(ext); len(stem) > limit {
		stem = strings.TrimRight(truncateUTF8(stem, limit), ".-")
	}
	if stem == "" {
		stem = "attachment"
	}
	return stem + ext
}

// appendExtension adds ext to a sanitized name, trimming the name so the
// result stays within maxAttachmentNameBytes.
func appendExtension(name, ext string) string {
	if limit := maxAttachmentNameBytes - len(ext); len(name) > limit {
		name = strings.TrimRight(truncateUTF8(name, limit), ".-")
	}
	return name + ext
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// attachmentMarkdown embeds images and links everything else.
func attachmentMarkdown(name, url string, image bool) string {
	if image {
		return "![" + name + "](" + url + ")"
	}
	return "[" + name + "](" + url + ")"
}

// UploadAttachment validates a comment attachment and stores it with the PR's
// SCM provider, returning a URL and the markdown that embeds it in a comment.
func (s *ActionService) UploadAttachment(ctx context.Context, request AttachmentRequest) (AttachmentResult, error) {
	prNumber, err := parsePRNumber(request.PRID)
	if err != nil || strings.TrimSpace(request.PRURL) == "" {
		return AttachmentResult{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	file, err := validateAttachment(request.Name, request.ContentType, request.Data)
	if err != nil {
		return AttachmentResult{}, err
	}
	if s.store == nil || s.uploader == nil {
		return AttachmentResult{}, errors.New("pr: attachment action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, request.PRURL)
	if err != nil {
		return AttachmentResult{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return AttachmentResult{}, ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return AttachmentResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}
	uploaded, err := s.uploader.UploadAttachment(ctx, ports.SCMAttachmentRequest{
		PR: ref, Name: file.name, ContentType: file.contentType, Data: request.Data,
	})
	if err != nil {
		return AttachmentResult{}, mapMutationError("upload pull request attachment", err)
	}
	if strings.TrimSpace(uploaded.URL) == "" {
		return AttachmentResult{}, errors.New("upload pull request attachment: provider returned no URL")
	}
	markdown := uploaded.Markdown
	if markdown == "" {
		markdown = attachmentMarkdown(file.name, uploaded.URL, file.image)
	}
	return AttachmentResult{URL: uploaded.URL, Name: file.name, Markdown: markdown}, nil
}
