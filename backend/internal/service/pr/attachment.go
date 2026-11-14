package pr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const MaxAttachmentBytes = 10 << 20

const maxAttachmentNameBytes = 100

type attachmentType struct {
	extensions []string
	image      bool
	sniff      func(data []byte, sniffed string) bool
}

func sniffedAs(want string) func([]byte, string) bool {
	return func(_ []byte, sniffed string) bool { return sniffed == want }
}

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

type validatedAttachment struct {
	name        string
	contentType string
	image       bool
}

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

const maxCommentAttachments = 10

const MaxCommentAttachmentsBytes = 25 << 20

var attachmentLink = regexp.MustCompile(`\]\(attachment:(\d+)\)`)

func attachmentLinks(body string) [][]int {
	code := markdownCodeRanges(body)
	var links [][]int
	for _, link := range attachmentLink.FindAllStringSubmatchIndex(body, -1) {
		inCode := false
		for _, span := range code {
			if link[0] >= span[0] && link[1] <= span[1] {
				inCode = true
				break
			}
		}
		if !inCode {
			links = append(links, link)
		}
	}
	return links
}

func commentAttachments(body string, inputs []CommentAttachment) ([]ports.SCMAttachmentFile, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > maxCommentAttachments {
		return nil, fmt.Errorf("%w: a comment takes at most %d files", ErrInvalidAttachment, maxCommentAttachments)
	}
	files := make([]ports.SCMAttachmentFile, 0, len(inputs))
	total := 0
	for _, in := range inputs {
		file, err := validateAttachment(in.Name, in.ContentType, in.Data)
		if err != nil {
			return nil, err
		}
		total += len(in.Data)
		files = append(files, ports.SCMAttachmentFile{Name: file.name, ContentType: file.contentType, Data: in.Data})
	}
	if total > MaxCommentAttachmentsBytes {
		return nil, fmt.Errorf("%w: a comment's files exceed %d bytes together", ErrAttachmentTooLarge, MaxCommentAttachmentsBytes)
	}
	linked := make([]bool, len(inputs))
	for _, link := range attachmentLinks(body) {
		i, err := strconv.Atoi(body[link[2]:link[3]])
		if err != nil || i >= len(inputs) {
			return nil, fmt.Errorf("%w: the comment links a file it doesn't have", ErrInvalidAttachment)
		}
		linked[i] = true
	}
	for i, ok := range linked {
		if !ok {
			return nil, fmt.Errorf("%w: file %d is not linked from the comment", ErrInvalidAttachment, i)
		}
	}
	return files, nil
}

func linkAttachments(body string, urls []string) string {
	var out strings.Builder
	last := 0
	for _, link := range attachmentLinks(body) {
		i, _ := strconv.Atoi(body[link[2]:link[3]])
		out.WriteString(body[last:link[0]])
		out.WriteString("](" + urls[i] + ")")
		last = link[1]
	}
	out.WriteString(body[last:])
	return out.String()
}

func (s *ActionService) AttachmentAccess(ctx context.Context, prID, prURL string) (AttachmentAccessResult, error) {
	prNumber, err := parsePRNumber(prID)
	if err != nil || strings.TrimSpace(prURL) == "" {
		return AttachmentAccessResult{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if s.store == nil {
		return AttachmentAccessResult{}, errors.New("pr: attachment action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, prURL)
	if err != nil {
		return AttachmentAccessResult{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return AttachmentAccessResult{}, ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return AttachmentAccessResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	if s.attachments == nil {
		return AttachmentAccessResult{Reason: attachmentsUnsupported}, nil
	}
	access, err := s.attachments.AttachmentAccess(ctx, ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL})
	if err != nil {
		if errors.Is(err, ports.ErrSCMUnsupported) {
			return AttachmentAccessResult{Reason: attachmentsUnsupported}, nil
		}
		return AttachmentAccessResult{}, mapAttachmentError("check attachment access", err)
	}
	return AttachmentAccessResult{Allowed: access.Allowed, Location: access.Location, Reason: access.Reason}, nil
}

const attachmentsUnsupported = "unsupported"

func (s *ActionService) storeCommentAttachments(ctx context.Context, ref ports.SCMPRRef, body string, files []ports.SCMAttachmentFile) (string, string, error) {
	if s.attachments == nil {
		return "", "", fmt.Errorf("%w: attachments", ErrUnsupported)
	}
	access, err := s.attachments.AttachmentAccess(ctx, ref)
	if err != nil {
		return "", "", mapAttachmentError("check attachment access", err)
	}
	if !access.Allowed {
		return "", "", ErrAttachmentsForbidden
	}
	stored, err := s.attachments.StoreAttachments(ctx, ports.SCMAttachmentBatch{PR: ref, Files: files})
	if err != nil {
		return "", "", mapAttachmentError("store comment attachments", err)
	}
	if len(stored.URLs) != len(files) {
		s.discardAttachments(ctx, ref, stored.Handle)
		return "", "", fmt.Errorf("store comment attachments: provider returned %d URLs for %d files", len(stored.URLs), len(files))
	}
	linked := linkAttachments(body, stored.URLs)
	if len(linked) > maxCommentBodyBytes {
		s.discardAttachments(ctx, ref, stored.Handle)
		return "", "", fmt.Errorf("%w: comment body exceeds %d bytes with its file links", ErrInvalidComment, maxCommentBodyBytes)
	}
	return linked, stored.Handle, nil
}

func (s *ActionService) discardAttachments(ctx context.Context, ref ports.SCMPRRef, handle string) {
	if handle == "" || s.attachments == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.attachments.DiscardAttachments(ctx, ref, handle); err != nil && s.logger != nil {
		s.logger.Warn("discard unposted comment attachments failed", "pr", ref.URL, "handle", handle, "err", err)
	}
}

func mapAttachmentError(op string, err error) error {
	if errors.Is(err, ports.ErrSCMForbidden) {
		return fmt.Errorf("%w: %w", ErrAttachmentsForbidden, err)
	}
	return mapMutationError(op, err)
}
