package linkpreview

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseHeadSkipsScriptBodies(t *testing.T) {
	doc := []byte(`<html><head>
<script>
const fake = '<meta property="og:title" content="Script Title">';
document.write('<meta property="og:description" content="Script Description">');
</script>
<meta property="og:title" content="Real Title">
<meta property="og:description" content="Real Description">
</head><body></body></html>`)

	m := parseHead(doc)
	if m.ogTitle != "Real Title" {
		t.Errorf("ogTitle = %q, want the tag after the script body to win", m.ogTitle)
	}
	if m.ogDescription != "Real Description" {
		t.Errorf("ogDescription = %q, want the tag after the script body to win", m.ogDescription)
	}
}

func TestParseHeadSkipsStyleBodies(t *testing.T) {
	doc := []byte(`<html><head>
<style>
.x::before { content: '<meta property="og:title" content="Style Title">'; }
</style>
<meta property="og:title" content="Real Title">
</head><body></body></html>`)

	m := parseHead(doc)
	if m.ogTitle != "Real Title" {
		t.Errorf("ogTitle = %q, want the tag after the style body to win", m.ogTitle)
	}
}

func TestParseHeadUnterminatedCommentDoesNotSwallowRest(t *testing.T) {
	doc := []byte(`<html><head>
<!-- a comment that never closes
<meta property="og:title" content="Still Found">
<meta name="description" content="Also Found">
</head><body></body></html>`)

	m := parseHead(doc)
	if m.ogTitle != "Still Found" {
		t.Errorf("ogTitle = %q, want tags after an unterminated comment to be extracted", m.ogTitle)
	}
	if m.description != "Also Found" {
		t.Errorf("description = %q, want tags after an unterminated comment to be extracted", m.description)
	}
}

func TestParseHeadUnterminatedScriptSkipsToEnd(t *testing.T) {
	doc := []byte(`<html><head>
<meta property="og:title" content="Before Script">
<script>const s = '<meta property="og:description" content="Inside Script">';`)

	m := parseHead(doc)
	if m.ogTitle != "Before Script" {
		t.Errorf("ogTitle = %q", m.ogTitle)
	}
	if m.ogDescription != "" {
		t.Errorf("ogDescription = %q, want script content ignored even without a close tag", m.ogDescription)
	}
}

func TestPreviewConcurrentSameURLFetchesOnce(t *testing.T) {
	hits := &atomic.Int32{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		// Slow enough that every concurrent caller overlaps the first fetch.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head><title>T</title></head><body></body></html>`)
	}))
	defer origin.Close()

	svc := New(origin.Client())
	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Preview(context.Background(), origin.URL+"/page")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("origin hits = %d, want 1 (singleflight dedup)", got)
	}
}

// A tag byte that is neither '>' nor '/' nor a name character (a stray quote,
// '=' or '<') must not stall the parser: the daemon parses hovered pages on
// request goroutines with no deadline, so a non-advancing loop spins a core
// forever and survives the request. Observed 2026-10-04: three hovers, three
// cores, until the daemon was restarted.
func TestParseHeadAdvancesPastStrayTagBytes(t *testing.T) {
	cases := map[string]string{
		"stray quote after attribute": `<html><head><meta name="description" content="x""><title>T</title></head><body></body></html>`,
		"lone equals sign":            `<html><head><link rel="icon" = href="/f.ico"><title>T</title></head></html>`,
		"angle bracket inside tag":    `<html><head><title <>T</title></head></html>`,
		"stray quote at tag start":    `<html><head><meta "><title>T</title></head></html>`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan headMeta, 1)
			go func() { done <- parseHead([]byte(doc)) }()
			select {
			case m := <-done:
				if m.title != "T" {
					t.Fatalf("title = %q, want %q", m.title, "T")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("parseHead did not return within 2s: parser loop does not advance")
			}
		})
	}
}

// htmlOrigin serves a titled page to /page and counts hits.
func htmlOrigin(t *testing.T, wait <-chan struct{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if wait != nil {
			<-wait
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head><title>Real</title></head><body></body></html>`)
	}))
	t.Cleanup(origin.Close)
	return origin, hits
}

func waitForHits(t *testing.T, hits *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("origin hits = %d, want %d", hits.Load(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Leaving a link must cost only that hover: the page was healthy, so the next
// hover has to get the page, not a cached failure.
func TestPreviewAbandonedHoverDoesNotPoisonCache(t *testing.T) {
	release := make(chan struct{})
	origin, hits := htmlOrigin(t, release)
	svc := New(origin.Client())

	ctx, cancel := context.WithCancel(context.Background())
	abandoned := make(chan error, 1)
	go func() {
		_, err := svc.Preview(ctx, origin.URL+"/page")
		abandoned <- err
	}()
	waitForHits(t, hits, 1)
	cancel()
	select {
	case err := <-abandoned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned caller err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("abandoned caller kept waiting for the upstream")
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		p, err := svc.Preview(context.Background(), origin.URL+"/page")
		if err == nil {
			if p.Title != "Real" {
				t.Fatalf("title = %q", p.Title)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("next hover never recovered: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("origin hits = %d, want 1 (the abandoned fetch fills the cache)", got)
	}
}

// A second hover that joined the fetch must not fail because the first left.
func TestPreviewJoinedCallerSurvivesFirstCallerLeaving(t *testing.T) {
	release := make(chan struct{})
	origin, hits := htmlOrigin(t, release)
	svc := New(origin.Client())

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := svc.Preview(ctx, origin.URL+"/page")
		first <- err
	}()
	waitForHits(t, hits, 1)

	joined := make(chan error, 1)
	go func() {
		_, err := svc.Preview(context.Background(), origin.URL+"/page")
		joined <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-first
	close(release)

	select {
	case err := <-joined:
		if err != nil {
			t.Fatalf("joined caller err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("joined caller never returned")
	}
}

func TestPreviewStopsReadingAtHeadClose(t *testing.T) {
	bodyDone := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head><title>Fast head</title></head><body>`)
		w.(http.Flusher).Flush()
		<-bodyDone
	}))
	t.Cleanup(origin.Close)
	// Cleanups run last-first: release the handler before Close waits for it.
	t.Cleanup(func() { close(bodyDone) })

	svc := New(origin.Client())
	svc.fetchTimeout = 5 * time.Second
	started := time.Now()
	p, err := svc.Preview(context.Background(), origin.URL+"/page")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Title != "Fast head" {
		t.Fatalf("title = %q", p.Title)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Preview waited %s for a body it does not need", elapsed)
	}
}

// oneByteReader forces the head marker to straddle reads.
type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }

func TestReadHeadFindsMarkerAcrossReads(t *testing.T) {
	doc := "<head><title>T</title></HEAD>" + strings.Repeat("x", 4096)
	got, err := readHead(oneByteReader{strings.NewReader(doc)})
	if err != nil {
		t.Fatalf("readHead: %v", err)
	}
	if want := len("<head><title>T</title></HEAD"); len(got) != want {
		t.Fatalf("read %d bytes, want it to stop at the close tag (%d)", len(got), want)
	}
}

func TestPreviewHungUpstreamIsBounded(t *testing.T) {
	release := make(chan struct{})
	origin, _ := htmlOrigin(t, release)
	t.Cleanup(func() { close(release) })

	svc := New(origin.Client())
	svc.fetchTimeout = 50 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		_, err := svc.Preview(context.Background(), origin.URL+"/page")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrFetchFailed) {
			t.Fatalf("err = %v, want ErrFetchFailed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hung upstream was not bounded by the fetch timeout")
	}
}

type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) { panic("fetch bug") }

// The shared fetch runs where no HTTP recover reaches it, so without its own
// recover this panic would end the test binary, as it would the daemon.
func TestPreviewPanicBecomesFetchFailure(t *testing.T) {
	svc := New(&http.Client{Transport: panicTransport{}})
	if _, err := svc.Preview(context.Background(), "http://example.test/page"); !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("err = %v, want ErrFetchFailed", err)
	}
}

func TestPreviewFragmentsShareOneFetch(t *testing.T) {
	origin, hits := htmlOrigin(t, nil)
	svc := New(origin.Client())
	for _, fragment := range []string{"#issuecomment-1", "#issuecomment-2", ""} {
		if _, err := svc.Preview(context.Background(), origin.URL+"/page"+fragment); err != nil {
			t.Fatalf("Preview(%q): %v", fragment, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("origin hits = %d, want 1", got)
	}
}

func TestPreviewFailureCacheWindows(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	hits := &atomic.Int32{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/binary" {
			w.Header().Set("Content-Type", "application/pdf")
			return
		}
		if code := int(status.Load()); code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head><title>Back</title></head></html>`)
	}))
	t.Cleanup(origin.Close)

	now := time.Unix(1_000_000, 0)
	svc := New(origin.Client())
	svc.now = func() time.Time { return now }

	if _, err := svc.Preview(context.Background(), origin.URL+"/page"); !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("err = %v, want ErrFetchFailed", err)
	}
	status.Store(http.StatusOK)

	now = now.Add(fetchFailureTTL - time.Second)
	if _, err := svc.Preview(context.Background(), origin.URL+"/page"); !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("within window err = %v, want the cached failure", err)
	}
	now = now.Add(2 * time.Second)
	p, err := svc.Preview(context.Background(), origin.URL+"/page")
	if err != nil || p.Title != "Back" {
		t.Fatalf("after window got %+v, %v; want the recovered page", p, err)
	}

	if _, err := svc.Preview(context.Background(), origin.URL+"/binary"); err == nil {
		t.Fatal("binary URL should fail")
	}
	before := hits.Load()
	now = now.Add(fetchFailureTTL + time.Second)
	if _, err := svc.Preview(context.Background(), origin.URL+"/binary"); err == nil {
		t.Fatal("binary URL should fail")
	}
	if got := hits.Load(); got != before {
		t.Fatalf("non-HTML result should outlive the fetch failure window; hits %d -> %d", before, got)
	}
}
