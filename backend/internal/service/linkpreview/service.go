// Package linkpreview fetches and parses external page metadata (Open Graph
// tags with HTML fallbacks) server-side so the CSP-locked desktop renderer can
// show a hover preview card for a link without fetching cross-origin itself.
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
	// fetchTimeout bounds the whole upstream round-trip. A hover preview must
	// not hang the daemon's per-request budget on a slow origin.
	fetchTimeout = 8 * time.Second
	// maxBodyBytes caps how much of a page is read when it never closes its
	// head; a page that does close it is read only that far.
	maxBodyBytes = 2 << 20 // 2 MiB
	// readChunkBytes is the read size while scanning for the end of the head.
	readChunkBytes = 32 << 10
	// maxRedirects bounds redirect chains so a loop cannot burn the timeout.
	maxRedirects = 10
	// userAgent identifies as a crawler: many sites only emit OG tags to bots.
	userAgent = "Mozilla/5.0 (compatible; AOLinkPreviewBot/1.0)"

	// successTTL is how long a fetched preview is reused.
	successTTL = 30 * time.Minute
	// fetchFailureTTL is the window for cached fetch failures, so a transient
	// origin error neither hammers the site nor hides a recovery for long.
	fetchFailureTTL = 30 * time.Second
	// notHTMLTTL is longer: a URL that serves a binary rarely stops doing so.
	notHTMLTTL = 2 * time.Minute
	// maxCacheEntries bounds the in-memory cache; URLs are user-driven, so the
	// map must not grow without limit.
	maxCacheEntries = 256
)

// ErrFetchFailed marks any upstream transport or HTTP-status failure. It is a
// sentinel rather than an apierr because the controller renders it as 502,
// which the apierr.Kind vocabulary has no Kind for.
var ErrFetchFailed = errors.New("linkpreview: upstream fetch failed")

// Preview is the metadata extracted from one page. Only URL is guaranteed;
// every other field is empty when the page does not provide it.
type Preview struct {
	URL         string
	Title       string
	Description string
	ImageURL    string
	SiteName    string
	FaviconURL  string
}

// failureKind records which class of failure a cache entry carries so a cached
// failure replays the same wire response.
type failureKind int

const (
	failureNone failureKind = iota
	failureFetch
	failureNotFound
)

// cacheEntry is one cached outcome — a successful preview or a failure class —
// stamped with the instant it was produced. Lookups compare fetchedAt against
// the class's TTL; an expired entry is a miss (lazy eviction).
type cacheEntry struct {
	preview   Preview
	failure   failureKind
	fetchedAt time.Time
}

// Service fetches pages and caches the extracted metadata. It is safe for
// concurrent use.
type Service struct {
	client *http.Client

	// fetchTimeout bounds one detached fetch; tests shorten it.
	fetchTimeout time.Duration

	// now defaults to time.Now; tests override it so TTL assertions are fast.
	now func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry

	// flight dedups concurrent fetches of the same URL so a burst of hovers
	// costs one upstream round-trip, not one per caller.
	flight singleflight.Group
}

// New returns a Service. A nil client gets the default: a short timeout, a
// redirect limit, and the standard transport.
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

// Preview returns the cached or freshly fetched metadata for rawURL. rawURL
// must be an absolute http or https URL; anything else is a 400-class error.
// Failures are cached under a shorter TTL than successes. Concurrent calls
// for the same URL share a single upstream fetch via singleflight.
//
// The shared fetch is detached from every caller: leaving a link cancels only
// that caller's wait, so one hover ending cannot fail another hover that joined
// it, cannot cache a failure for a healthy page, and still warms the cache for
// the next hover.
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

// result replays a cached entry as a (Preview, error) pair.
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
	// A fragment never reaches the server, so links to different anchors of one
	// page (GitHub comment links, for example) share one fetch and cache entry.
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

var errNotHTML = apierr.NotFound("LINK_PREVIEW_NOT_FOUND", "The URL does not serve an HTML page to preview")

// fetchRecovered turns a panic into an ordinary failure. The shared fetch runs
// on singleflight's own goroutine, out of reach of the HTTP server's recover,
// and DoChan re-raises a panic where nothing can catch it, so untrusted page
// content could otherwise end the daemon.
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

// fetch retrieves the page and extracts its metadata. The returned error is
// already the value the controller should render.
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
		// No Content-Type at all: do not guess — a binary would otherwise be
		// cached for 30 minutes as an empty "success" card.
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

	// Resolve relative references — and the preview's own URL — against the
	// final post-redirect URL, so a shortener (t.co etc.) shows the target's
	// site, not the shortener's.
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
	if meta.favicon != "" {
		p.FaviconURL = resolveReference(base, meta.favicon)
	} else {
		p.FaviconURL = base.Scheme + "://" + base.Host + "/favicon.ico"
	}
	return p, failureNone, nil
}

// readHead reads the page until its head closes, so a heavy or slow body costs
// nothing, and stops at maxBodyBytes for a page that never closes it. It stops
// at </head only: parseHead would keep looking past a "<body" inside a script.
func readHead(r io.Reader) ([]byte, error) {
	var doc []byte
	chunk := make([]byte, readChunkBytes)
	for len(doc) < maxBodyBytes {
		n, err := r.Read(chunk[:min(len(chunk), maxBodyBytes-len(doc))])
		// Rescan the tail of the previous chunk: the marker can straddle two reads.
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

// resolveReference resolves a possibly-relative URL from a tag against the
// page URL. An unparseable reference resolves to "" rather than a bogus URL.
func resolveReference(base *url.URL, ref string) string {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

// lookup returns a cached entry when one exists and is still fresh.
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

// store inserts an entry, evicting expired entries — and, if the cache is
// still full, the oldest — to stay within maxCacheEntries.
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

// headClose marks the end of the only region the scanner reads.
const headClose = "</head"

// --- head scanning ------------------------------------------------------------
//
// parseHead is a small targeted scanner, not a general HTML parser: the
// backend deliberately has no HTML-parsing dependency, and the tags of
// interest (<meta>, <link>, <title>) are void or simple-text elements that a
// lenient byte scan handles. Only the region before </head> (or <body>) is
// scanned.

// headMeta carries every candidate value parseHead finds; the caller picks
// the winners and applies fallbacks.
type headMeta struct {
	ogTitle       string
	ogDescription string
	ogImage       string
	ogSiteName    string
	title         string
	description   string
	favicon       string
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
				// Unterminated comment: skip only the opener so tags after it
				// are still found, rather than abandoning the rest of the head.
				i += len("<!--")
				continue
			}
			i += 4 + end + 3
		case tagOpens(rest, "script"):
			// Raw-text elements can contain literal "<meta ...>" strings that
			// must never win via first-writer-wins.
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

// headRegion cuts the document at </head> (or <body> when the head was never
// closed) so a 2 MiB body is not scanned for tags that cannot be there.
func headRegion(doc []byte) []byte {
	if i := indexFold(doc, headClose); i >= 0 {
		return doc[:i]
	}
	if i := indexFold(doc, "<body"); i >= 0 {
		return doc[:i]
	}
	return doc
}

// skipRawTextElement returns the offset just past the close tag of the
// raw-text element (script/style) opening at doc[start]. An unterminated
// element skips to the end of the document.
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

// indexFold finds sub in b, ASCII case-insensitively.
func indexFold(b []byte, sub string) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		if b[i] == sub[0] && bytes.EqualFold(b[i:i+len(sub)], []byte(sub)) {
			return i
		}
	}
	return -1
}

// tagOpens reports whether rest starts with "<name" followed by a tag boundary
// (whitespace, '/', or '>'), so "<metadata" does not match "meta".
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

// parseTag reads the attributes of the tag opening at doc[start] ('<') and
// returns them lowercased-name → unescaped-value, plus the offset just past
// the closing '>' (or len(doc) if the tag is unterminated).
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
			// Not '>', '/' or a name byte (a stray quote, '=' or '<'): skip it.
			// Without this the loop re-reads the same byte forever, and the
			// request goroutine spins a core until the daemon restarts.
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

// readUntilClose returns the text between from and the next "</name" (or the
// end of the document), plus the offset of that closing tag.
func readUntilClose(doc []byte, from int, name string) (string, int) {
	if i := indexFold(doc[from:], "</"+name); i >= 0 {
		return string(doc[from : from+i]), from + i
	}
	return string(doc[from:]), len(doc)
}

// applyMeta folds one <meta> tag into the candidate set. First writer wins so
// a duplicate tag cannot clobber the page's canonical value.
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

// applyLink folds one <link> tag into the candidate set, keeping only icon
// references.
func applyLink(m *headMeta, attrs map[string]string) {
	if m.favicon != "" || attrs["href"] == "" {
		return
	}
	for _, token := range strings.Fields(strings.ToLower(attrs["rel"])) {
		if token == "icon" || token == "shortcut" {
			m.favicon = attrs["href"]
			return
		}
	}
}

// cleanText unescapes entities and trims surrounding whitespace from text
// destined for the preview card.
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
