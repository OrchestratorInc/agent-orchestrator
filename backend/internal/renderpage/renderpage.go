// Package renderpage turns a stored agent render into the document the
// sandboxed render route serves: the page with AO's theme and bootstrap.
package renderpage

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

//go:embed render_bootstrap.js
var bootstrapJS string

// The variables agent pages style against (documented in commands/render.md).
// The host replaces this rule before first paint with the reader's live theme.
const defaultThemeCSS = `:root{color-scheme:dark;--background:oklch(0.210 0.002 250);--foreground:oklch(0.970 0.002 250);--muted:oklch(0.295 0.002 250);--muted-foreground:oklch(0.720 0.002 250);--card:oklch(0.250 0.002 250);--card-foreground:oklch(0.970 0.002 250);--popover:oklch(0.300 0.002 250);--border:oklch(1 0 0 / 7%);--border-strong:oklch(1 0 0 / 4%);--primary:oklch(0.900 0.002 250);--primary-foreground:oklch(0.210 0.002 250);--accent:oklch(0.900 0.002 250);--accent-foreground:oklch(0.210 0.002 250);--success:#4ade80;--warning:#f06445;--destructive:oklch(0.704 0.191 22.216);--code:#79b0dc;--link:#79b0dc;--chart-1:#79b0dc;--chart-2:#2dd4bf;--chart-3:#fbbf24;--chart-4:#c084fc;--chart-5:#fb7185;--chart-6:#a3e635;--radius:0.5rem;--font-sans:ui-sans-serif,system-ui,sans-serif;--font-mono:ui-monospace,Menlo,Consolas,monospace}` +
	`@media (prefers-color-scheme: light){:root{color-scheme:light;--background:oklch(0.97 0 0);--foreground:oklch(0.18 0 0);--muted:oklch(0.935 0 0);--muted-foreground:oklch(0.5 0 0);--card:oklch(0.975 0 0);--card-foreground:oklch(0.18 0 0);--popover:oklch(0.99 0 0);--border:oklch(0.91 0 0);--border-strong:oklch(0.86 0 0);--primary:oklch(0.24 0 0);--primary-foreground:oklch(0.97 0 0);--accent:oklch(0.24 0 0);--accent-foreground:oklch(0.97 0 0);--success:#16a34a;--warning:#c2412d;--destructive:oklch(0.577 0.245 27.325);--code:#304c83;--link:#304c83;--chart-1:#304c83;--chart-2:#0d9488;--chart-3:#d97706;--chart-4:#9333ea;--chart-5:#e11d48;--chart-6:#65a30d}}`

// The inline frame grows to fit the page, so a scrollbar inside the reply would
// read as a box within the thread; it stays hidden. Fullscreen shows it again
// (render_bootstrap.js). The page's own CSS overrides all of this.
const baseCSS = `html{background:var(--background);color:var(--foreground);font-family:var(--font-sans);font-size:14px;line-height:1.5;-webkit-font-smoothing:antialiased;scrollbar-width:none}html::-webkit-scrollbar{display:none}body{margin:0}code,kbd,pre,samp{font-family:var(--font-mono)}` +
	// Pages written for Codex's visualize skill use these token names; they map onto the AO theme.
	// This rule follows #ao-theme, so each alias resolves to the live host theme.
	`:root{--viz-series-1:var(--chart-1);--viz-series-2:var(--chart-2);--viz-series-3:var(--chart-3);--viz-series-4:var(--chart-4);--viz-series-5:var(--chart-5);--viz-series-6:var(--chart-6);--viz-text:var(--foreground);--viz-muted:var(--muted-foreground);--viz-bg:var(--background);--viz-panel:var(--card);--viz-border:var(--border);--viz-accent:var(--accent);--viz-accent-text:var(--accent-foreground);--viz-accent-bg:var(--muted);--viz-warning:var(--warning);--blue:var(--chart-1);--green:var(--success);--orange:var(--warning);--yellow:var(--chart-3);--purple:var(--chart-4);--red:var(--destructive)}`

var (
	// A BOM, whitespace, and comments may precede the doctype. Anything inserted
	// before the doctype would drop the page into quirks mode.
	leadingDoctype = regexp.MustCompile(`(?is)^\x{FEFF}?(?:\s|<!--.*?-->)*<!doctype[^>]*>`)
	pageViewport   = regexp.MustCompile(`(?i)<meta\s[^>]*name\s*=\s*["']?viewport`)
	// Characters a file system refuses, and control characters.
	unsafeFileNameChars = regexp.MustCompile(`[\\/:*?"<>|\p{Cc}]+`)
)

// FileName is the file a saved render is written to: its title, without
// characters a file system refuses, whitespace collapsed, capped at 120 runes.
// A port of renderFileName in frontend/src/renderer/lib/render-frame.ts.
func FileName(title string) string {
	// JS \s, which the renderer collapses, also matches U+FEFF.
	isSpace := func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }
	name := strings.Join(strings.FieldsFunc(unsafeFileNameChars.ReplaceAllString(title, " "), isSpace), " ")
	if runes := []rune(name); len(runes) > 120 {
		name = strings.TrimSpace(string(runes[:120]))
	}
	if name == "" {
		name = "Page"
	}
	return name + ".html"
}

// Version names the bootstrap this build injects. The render route puts it in
// the ETag, so a page fetched before a daemon upgrade is not reused after it.
var Version = func() string {
	sum := sha256.Sum256([]byte(inject("")))
	return hex.EncodeToString(sum[:])[:12]
}()

const (
	themeStyleOpen = `<style id="ao-theme">`
	// viewportMeta is the viewport AO adds to a page with none. Renders stored
	// before the bootstrap moved to serve time have it ahead of their block.
	viewportMeta = `<meta name="viewport" content="width=device-width, initial-scale=1">`
)

// Document is the page the render route serves for stored bytes. Renders are
// stored raw, but ones published before the bootstrap moved to serve time
// carry an earlier copy. That block is AO's own and contiguous: it starts
// with the theme style right after the doctype (and AO's viewport meta),
// where it was injected, and ends at the first </script>, because the
// bootstrap never contains one. It is replaced by the current bootstrap. The
// same markup anywhere else is the agent's, and stays.
func Document(stored []byte) []byte {
	page := string(stored)
	at := 0
	if loc := leadingDoctype.FindStringIndex(page); loc != nil {
		at = loc[1]
	}
	if strings.HasPrefix(page[at:], viewportMeta) {
		at += len(viewportMeta)
	}
	if strings.HasPrefix(page[at:], themeStyleOpen) {
		if end := strings.Index(page[at:], "</script>"); end >= 0 {
			page = page[:at] + page[at+end+len("</script>"):]
		}
	}
	return []byte(inject(page))
}

// inject places the theme style and bootstrap script ahead of the page's own
// markup. Right after the doctype the HTML parser opens the implied <head> for
// them; the page's own <html> start tag then only merges its attributes and its
// <head> start tag is ignored, so the page's styles and scripts still come
// after the bootstrap. This needs neither an HTML tokenizer (golang.org/x/net
// is not a dependency) nor T3's backreference regex, which RE2 cannot express.
// The viewport meta follows the script, outside the block Document removes, so
// injecting twice yields the same document.
func inject(page string) string {
	var b strings.Builder
	b.WriteString(themeStyleOpen + defaultThemeCSS + `</style>`)
	b.WriteString(`<style>` + baseCSS + `</style>`)
	b.WriteString(`<script>` + bootstrapJS + `</script>`)
	if !pageViewport.MatchString(page) {
		b.WriteString(viewportMeta)
	}
	if loc := leadingDoctype.FindStringIndex(page); loc != nil {
		return page[:loc[1]] + b.String() + page[loc[1]:]
	}
	return "<!doctype html>" + b.String() + page
}
