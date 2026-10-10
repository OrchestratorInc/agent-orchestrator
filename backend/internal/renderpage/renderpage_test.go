package renderpage

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestInjectLandsAfterTheDoctype(t *testing.T) {
	cases := map[string]struct{ page, wantPrefix, wantSuffix string }{
		"doctype": {
			page:       "<!DOCTYPE html><html lang=\"en\"><head><title>x</title></head><body>b</body></html>",
			wantPrefix: "<!DOCTYPE html><style id=\"ao-theme\">",
			wantSuffix: "<html lang=\"en\"><head><title>x</title></head><body>b</body></html>",
		},
		"bom and comment before doctype": {
			page:       "\xef\xbb\xbf<!-- made by an agent -->\n<!doctype html><p>x</p>",
			wantPrefix: "\xef\xbb\xbf<!-- made by an agent -->\n<!doctype html><style id=\"ao-theme\">",
			wantSuffix: "<p>x</p>",
		},
		"no doctype": {
			page:       "<div>x</div>",
			wantPrefix: "<!doctype html><style id=\"ao-theme\">",
			wantSuffix: "<div>x</div>",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := inject(tc.page)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("prefix = %.160q", got)
			}
			if !strings.HasSuffix(got, tc.wantSuffix) {
				t.Fatalf("page body not preserved verbatim: %.160q", got[len(got)-min(len(got), 160):])
			}
			for _, want := range []string{`<meta name="viewport"`, "ui/notifications/size-changed", "ui/open-link", "ao-theme=", "--viz-series-1:var(--chart-1)", "--blue:var(--chart-1)"} {
				if !strings.Contains(got, want) {
					t.Errorf("bootstrap missing %q", want)
				}
			}
		})
	}
}

func TestInjectKeepsThePagesViewport(t *testing.T) {
	got := inject(`<!doctype html><meta name="viewport" content="width=500">`)
	if n := strings.Count(got, `name="viewport"`); n != 1 {
		t.Fatalf("viewport metas = %d, want the page's own only", n)
	}
}

func TestBootstrapCannotCloseItsOwnElements(t *testing.T) {
	if strings.Contains(strings.ToLower(bootstrapJS), "</script") {
		t.Fatal("bootstrap script would end its own <script> element")
	}
	if strings.Contains(strings.ToLower(defaultThemeCSS+baseCSS), "</style") {
		t.Fatal("bootstrap CSS would end its own <style> element")
	}
}

// What PublishRender stored before the bootstrap moved to serve time: the
// optional viewport meta, then the theme style, base style, and script.
const earlierStoredPage = "<!doctype html>" +
	`<meta name="viewport" content="width=device-width, initial-scale=1">` +
	`<style id="ao-theme">:root{--old-theme:1}</style><style>html{--old-base:1}</style>` +
	`<script>/* old bootstrap */ s.replace(/[;{}<>]/g, "")</script>` +
	`<html lang="en"><body><p>chart</p><script>draw()</script></body></html>`

func TestDocumentReplacesAnEarlierBootstrap(t *testing.T) {
	got := string(Document([]byte(earlierStoredPage)))
	if n := strings.Count(got, `<style id="ao-theme">`); n != 1 {
		t.Fatalf("theme styles = %d, want 1: %.300q", n, got)
	}
	for _, old := range []string{"--old-theme", "--old-base", "old bootstrap"} {
		if strings.Contains(got, old) {
			t.Errorf("earlier bootstrap %q survived", old)
		}
	}
	if !strings.Contains(got, bootstrapJS) {
		t.Error("current bootstrap missing")
	}
	if n := strings.Count(got, `name="viewport"`); n != 1 {
		t.Errorf("viewport metas = %d, want 1", n)
	}
	if !strings.HasSuffix(got, `<html lang="en"><body><p>chart</p><script>draw()</script></body></html>`) {
		t.Errorf("page body not preserved: %q", got[len(got)-min(len(got), 160):])
	}
}

// Only the block right after the doctype is AO's; the same markup later in the
// page is the agent's own (a page about renders, say), and stays.
func TestDocumentKeepsTheThemeMarkupInThePagesOwnContent(t *testing.T) {
	page := "<!doctype html><p>Before</p><pre>&lt;style id=\"ao-theme\"&gt;</pre>" +
		`<script>const t = '<style id="ao-theme">';</script><p>After</p><script>draw()</script>`
	got := string(Document([]byte(page)))
	if !strings.HasSuffix(got, page[len("<!doctype html>"):]) {
		t.Fatalf("page content changed: %.400q", got)
	}
}

func TestDocumentIsIdempotent(t *testing.T) {
	for _, page := range []string{
		"<p>x</p>",
		"<!doctype html><p>x</p><script>go()</script>",
		`<!doctype html><meta name="viewport" content="width=500"><p>x</p>`,
		earlierStoredPage,
	} {
		once := Document([]byte(page))
		if twice := Document(once); !bytes.Equal(once, twice) {
			t.Errorf("Document(Document(%.40q)) differs from Document once:\n once=%.300q\ntwice=%.300q", page, once, twice)
		}
	}
	if got := string(Document([]byte("<p>x</p>"))); got != inject("<p>x</p>") {
		t.Errorf("a raw page should only be injected: %.200q", got)
	}
}

func TestVersionIsAShortDigestOfTheBootstrap(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(Version) {
		t.Fatalf("Version = %q, want 12 hex chars", Version)
	}
}

func TestBootstrapStylesFullscreenAndScrollableFrames(t *testing.T) {
	// Fullscreen centers a width-capped top-level block (the page's own margins
	// win over this low-specificity rule) and shows the scrollbar; a scrollable
	// frame, AO's own field, shows the scrollbar alone.
	for _, want := range []string{
		`"body>*{margin-inline:auto}" + showScrollbar`,
		`showScrollbar = "html{scrollbar-width:auto}html::-webkit-scrollbar{display:block}"`,
		`scrollable ? showScrollbar : ""`,
		`"ao-display"`, `"fullscreen"`, ".displayMode", ".scrollable",
	} {
		if !strings.Contains(bootstrapJS, want) {
			t.Errorf("bootstrap missing %q", want)
		}
	}
}

// The cases mirror renderFileName in frontend/src/renderer/lib/render-frame.test.ts.
func TestFileNameMatchesTheRenderer(t *testing.T) {
	for title, want := range map[string]string{
		"Turns by day":                          "Turns by day.html",
		"Q3: a/b\\c *?\"<>| \u0007 report\n":    "Q3 a b c report.html",
		"":                                      "Page.html",
		" /:*?\"<>| ":                           "Page.html",
		strings.Repeat("x", 119) + " tail":      strings.Repeat("x", 119) + ".html",
		strings.Repeat("y", 300):                strings.Repeat("y", 120) + ".html",
		strings.Repeat("é", 130):                strings.Repeat("é", 120) + ".html",
		"tab\there\u00a0and\u2003wide   spaces": "tab here and wide spaces.html",
		// JS \s matches U+FEFF; Go's unicode.IsSpace does not.
		"\ufeffTurns\ufeffby day\ufeff": "Turns by day.html",
	} {
		if got := FileName(title); got != want {
			t.Errorf("FileName(%q) = %q, want %q", title, got, want)
		}
	}
}
