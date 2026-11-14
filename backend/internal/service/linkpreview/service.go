package linkpreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

const (
	fetchTimeout   = 8 * time.Second
	maxBodyBytes   = 2 << 20
	readChunkBytes = 32 << 10
	maxRedirects   = 10
	userAgent      = "Mozilla/5.0 (compatible; AOLinkPreviewBot/1.0)"

	successTTL      = 30 * time.Minute
	fetchFailureTTL = 30 * time.Second
	notHTMLTTL      = 2 * time.Minute
	maxCacheEntries = 256
)

var ErrFetchFailed = errors.New("linkpreview: upstream fetch failed")

type Preview struct {
	URL            string
	Title          string
	Description    string
	ImageURL       string
	SiteName       string
	FaviconURL     string
	FaviconDarkURL string
}

type failureKind int

const (
	failureNone failureKind = iota
	failureFetch
	failureNotFound
)

type cacheEntry struct {
	preview   Preview
	failure   failureKind
	fetchedAt time.Time
}

type Service struct {
	client *http.Client

	fetchTimeout time.Duration

	now func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry

	flight singleflight.Group
}

func New(client *http.Client) *Service {
	if client == nil {
		client = &http.Client{
			Timeout: fetchTimeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return fmt.Errorf("linkpreview: stopped after %d redirects", maxRedirects)
				}
				return nil
			},
		}
	}
	return &Service{
		client:       client,
		fetchTimeout: fetchTimeout,
		now:          time.Now,
		entries:      map[string]cacheEntry{},
	}
}

func (s *Service) Preview(ctx context.Context, rawURL string) (Preview, error) {
	pageURL, err := validateURL(rawURL)
	if err != nil {
		return Preview{}, err
	}
	key := pageURL.String()
	if e, ok := s.lookup(key); ok {
		return e.result()
	}
	fetchCtx := context.WithoutCancel(ctx)
	results := s.flight.DoChan(key, func() (any, error) {
		detached, cancel := context.WithTimeout(fetchCtx, s.fetchTimeout)
		defer cancel()
		p, failure, fetchErr := s.fetchRecovered(detached, pageURL)
		s.store(key, cacheEntry{preview: p, failure: failure, fetchedAt: s.now()})
		return p, fetchErr
	})
	select {
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	case result := <-results:
		if result.Err != nil {
			return Preview{}, result.Err
		}
		p, ok := result.Val.(Preview)
		if !ok {
			return Preview{}, apierr.Internal("INTERNAL_ERROR", "unexpected link preview result type")
		}
		return p, nil
	}
}

func (e cacheEntry) result() (Preview, error) {
	switch e.failure {
	case failureNone:
		return e.preview, nil
	case failureNotFound:
		return Preview{}, errNotHTML
	default:
		return Preview{}, ErrFetchFailed
	}
}

func validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, apierr.Invalid("INVALID_URL", "url must be an absolute http or https URL", nil)
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

var errNotHTML = apierr.NotFound("LINK_PREVIEW_NOT_FOUND", "The URL does not serve an HTML page to preview")

func (s *Service) fetchRecovered(ctx context.Context, pageURL *url.URL) (p Preview, failure failureKind, err error) {
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		kind := telemetrymeta.PanicKind(rec)
		log.Printf("link preview fetch panic: %v\n%s", rec, debug.Stack())
		sentryobs.CapturePanic(context.Background(), map[string]string{
			"component":  "linkpreview",
			"operation":  "fetch_panic",
			"panic_kind": kind,
		}, telemetrymeta.Fingerprint("linkpreview", "fetch_panic", kind), telemetrymeta.SafeRoute{}, telemetrymeta.CurrentFailureSite())
		p, failure, err = Preview{}, failureFetch, fmt.Errorf("%w: fetch panicked", ErrFetchFailed)
	}()
	return s.fetch(ctx, pageURL)
}

func (s *Service) fetch(ctx context.Context, pageURL *url.URL) (Preview, failureKind, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), http.NoBody)
	if err != nil {
		return Preview{}, failureFetch, fmt.Errorf("%w: %w", ErrFetchFailed, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := s.client.Do(req)
	if err != nil {
		return Preview{}, failureFetch, fmt.Errorf("%w: %w", ErrFetchFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Preview{}, failureFetch, fmt.Errorf("%w: upstream status %d", ErrFetchFailed, resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		return Preview{}, failureNotFound, errNotHTML
	}
	mediatype, _, err := mime.ParseMediaType(ct)
	if err != nil || (mediatype != "text/html" && mediatype != "application/xhtml+xml") {
		return Preview{}, failureNotFound, errNotHTML
	}
	body, err := readHead(resp.Body)
	if err != nil {
		return Preview{}, failureFetch, fmt.Errorf("%w: %w", ErrFetchFailed, err)
	}

	base := resp.Request.URL
	meta := parseHead(body)
	p := Preview{
		URL:         base.String(),
		Title:       firstNonEmpty(meta.ogTitle, meta.title),
		Description: firstNonEmpty(meta.ogDescription, meta.description),
		SiteName:    meta.ogSiteName,
	}
	if meta.ogImage != "" {
		p.ImageURL = resolveReference(base, meta.ogImage)
	}
	if favicon := firstNonEmpty(meta.favicon, meta.faviconLight, meta.faviconDark); favicon != "" {
		p.FaviconURL = resolveReference(base, favicon)
	} else {
		p.FaviconURL = base.Scheme + "://" + base.Host + "/favicon.ico"
	}
	if meta.faviconDark != "" {
		p.FaviconDarkURL = resolveReference(base, meta.faviconDark)
	}
	return p, failureNone, nil
}

func readHead(r io.Reader) ([]byte, error) {
	var doc []byte
	chunk := make([]byte, readChunkBytes)
	for len(doc) < maxBodyBytes {
		n, err := r.Read(chunk[:min(len(chunk), maxBodyBytes-len(doc))])
		from := max(len(doc)-len(headClose), 0)
		doc = append(doc, chunk[:n]...)
		if indexFold(doc[from:], headClose) >= 0 {
			return doc, nil
		}
		if errors.Is(err, io.EOF) {
			return doc, nil
		}
		if err != nil {
			return doc, err
		}
	}
	return doc, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func resolveReference(base *url.URL, ref string) string {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

func (s *Service) lookup(key string) (cacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	if s.now().Sub(e.fetchedAt) > entryTTL(e.failure) {
		return cacheEntry{}, false
	}
	return e, true
}

func (s *Service) store(key string, e cacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= maxCacheEntries {
		now := s.now()
		for k, v := range s.entries {
			if now.Sub(v.fetchedAt) > entryTTL(v.failure) {
				delete(s.entries, k)
			}
		}
	}
	if len(s.entries) >= maxCacheEntries {
		var oldestKey string
		var oldest time.Time
		first := true
		for k, v := range s.entries {
			if first || v.fetchedAt.Before(oldest) {
				oldestKey, oldest, first = k, v.fetchedAt, false
			}
		}
		delete(s.entries, oldestKey)
	}
	s.entries[key] = e
}

func entryTTL(f failureKind) time.Duration {
	switch f {
	case failureNone:
		return successTTL
	case failureNotFound:
		return notHTMLTTL
	default:
		return fetchFailureTTL
	}
}

const headClose = "</head"

type headMeta struct {
	ogTitle       string
	ogDescription string
	ogImage       string
	ogSiteName    string
	title         string
	description   string
	favicon       string
	faviconLight  string
	faviconDark   string
}

func parseHead(doc []byte) headMeta {
	doc = headRegion(doc)
	var m headMeta
	i := 0
	for i < len(doc) {
		lt := bytes.IndexByte(doc[i:], '<')
		if lt < 0 {
			break
		}
		i += lt
		switch rest := doc[i:]; {
		case bytes.HasPrefix(rest, []byte("<!--")):
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				i += len("<!--")
				continue
			}
			i += 4 + end + 3
		case tagOpens(rest, "script"):
			i = skipRawTextElement(doc, i, "script")
		case tagOpens(rest, "style"):
			i = skipRawTextElement(doc, i, "style")
		case tagOpens(rest, "meta"):
			attrs, after := parseTag(doc, i)
			applyMeta(&m, attrs)
			i = after
		case tagOpens(rest, "link"):
			attrs, after := parseTag(doc, i)
			applyLink(&m, attrs)
			i = after
		case tagOpens(rest, "title"):
			_, tagEnd := parseTag(doc, i)
			text, after := readUntilClose(doc, tagEnd, "title")
			if m.title == "" {
				m.title = cleanText(text)
			}
			i = after
		default:
			i++
		}
	}
	return m
}

func headRegion(doc []byte) []byte {
	if i := indexFold(doc, headClose); i >= 0 {
		return doc[:i]
	}
	if i := indexFold(doc, "<body"); i >= 0 {
		return doc[:i]
	}
	return doc
}

func skipRawTextElement(doc []byte, start int, name string) int {
	_, tagEnd := parseTag(doc, start)
	closeAt := indexFold(doc[tagEnd:], "</"+name)
	if closeAt < 0 {
		return len(doc)
	}
	gt := bytes.IndexByte(doc[tagEnd+closeAt:], '>')
	if gt < 0 {
		return len(doc)
	}
	return tagEnd + closeAt + gt + 1
}

func indexFold(b []byte, sub string) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		if b[i] == sub[0] && bytes.EqualFold(b[i:i+len(sub)], []byte(sub)) {
			return i
		}
	}
	return -1
}

func tagOpens(rest []byte, name string) bool {
	if len(rest) < len(name)+2 || rest[0] != '<' {
		return false
	}
	if !bytes.EqualFold(rest[1:1+len(name)], []byte(name)) {
		return false
	}
	b := rest[1+len(name)]
	return b == '>' || b == '/' || b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func parseTag(doc []byte, start int) (map[string]string, int) {
	attrs := map[string]string{}
	i := start + 1
	for i < len(doc) && isNameByte(doc[i]) {
		i++
	}
	for i < len(doc) {
		i = skipSpaces(doc, i)
		if i >= len(doc) {
			break
		}
		if doc[i] == '>' {
			return attrs, i + 1
		}
		if doc[i] == '/' {
			i++
			continue
		}
		nameStart := i
		for i < len(doc) && isNameByte(doc[i]) {
			i++
		}
		if i == nameStart {
			i++
			continue
		}
		name := strings.ToLower(string(doc[nameStart:i]))
		i = skipSpaces(doc, i)
		value := ""
		if i < len(doc) && doc[i] == '=' {
			i++
			i = skipSpaces(doc, i)
			if i < len(doc) && (doc[i] == '"' || doc[i] == '\'') {
				quote := doc[i]
				i++
				valueStart := i
				for i < len(doc) && doc[i] != quote {
					i++
				}
				value = string(doc[valueStart:i])
				if i < len(doc) {
					i++
				}
			} else {
				valueStart := i
				for i < len(doc) && !isSpaceByte(doc[i]) && doc[i] != '>' {
					i++
				}
				value = string(doc[valueStart:i])
			}
		}
		if name != "" {
			attrs[name] = html.UnescapeString(value)
		}
	}
	return attrs, i
}

func readUntilClose(doc []byte, from int, name string) (string, int) {
	if i := indexFold(doc[from:], "</"+name); i >= 0 {
		return string(doc[from : from+i]), from + i
	}
	return string(doc[from:]), len(doc)
}

func applyMeta(m *headMeta, attrs map[string]string) {
	content := attrs["content"]
	if content == "" {
		return
	}
	key := attrs["property"]
	if key == "" {
		key = attrs["name"]
	}
	switch strings.ToLower(key) {
	case "og:title":
		if m.ogTitle == "" {
			m.ogTitle = cleanText(content)
		}
	case "og:description":
		if m.ogDescription == "" {
			m.ogDescription = cleanText(content)
		}
	case "og:image", "og:image:url", "og:image:secure_url":
		if m.ogImage == "" {
			m.ogImage = content
		}
	case "og:site_name":
		if m.ogSiteName == "" {
			m.ogSiteName = cleanText(content)
		}
	case "description":
		if m.description == "" {
			m.description = cleanText(content)
		}
	}
}

func applyLink(m *headMeta, attrs map[string]string) {
	if attrs["href"] == "" || !isIconRel(attrs["rel"]) {
		return
	}
	slot := &m.favicon
	media := strings.ToLower(strings.Join(strings.Fields(attrs["media"]), ""))
	switch {
	case strings.Contains(media, "prefers-color-scheme:dark"):
		slot = &m.faviconDark
	case strings.Contains(media, "prefers-color-scheme:light"):
		slot = &m.faviconLight
	}
	if *slot == "" {
		*slot = attrs["href"]
	}
}

func isIconRel(rel string) bool {
	for _, token := range strings.Fields(strings.ToLower(rel)) {
		if token == "icon" || token == "shortcut" {
			return true
		}
	}
	return false
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

func isNameByte(b byte) bool {
	return b == '-' || b == '_' || b == ':' || b == '.' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func skipSpaces(doc []byte, i int) int {
	for i < len(doc) && isSpaceByte(doc[i]) {
		i++
	}
	return i
}
