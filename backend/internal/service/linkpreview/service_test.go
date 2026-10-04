package linkpreview

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
