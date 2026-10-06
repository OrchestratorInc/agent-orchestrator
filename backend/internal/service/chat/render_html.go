package chat

import (
	_ "embed"
	"regexp"
	"strings"
)

//go:embed render_bootstrap.js
var renderBootstrapJS string

// The variables agent pages style against (documented in commands/render.md).
// The host replaces this rule before first paint with the reader's live theme.
const renderDefaultThemeCSS = `:root{color-scheme:dark;--background:oklch(0.210 0.002 250);--foreground:oklch(0.970 0.002 250);--muted:oklch(0.295 0.002 250);--muted-foreground:oklch(0.720 0.002 250);--card:oklch(0.250 0.002 250);--card-foreground:oklch(0.970 0.002 250);--popover:oklch(0.300 0.002 250);--border:oklch(1 0 0 / 7%);--border-strong:oklch(1 0 0 / 4%);--primary:oklch(0.900 0.002 250);--primary-foreground:oklch(0.210 0.002 250);--accent:oklch(0.900 0.002 250);--accent-foreground:oklch(0.210 0.002 250);--success:#4ade80;--warning:#f06445;--destructive:oklch(0.704 0.191 22.216);--code:#79b0dc;--link:#79b0dc;--chart-1:#79b0dc;--chart-2:#2dd4bf;--chart-3:#fbbf24;--chart-4:#c084fc;--chart-5:#fb7185;--chart-6:#a3e635;--radius:0.5rem;--font-sans:ui-sans-serif,system-ui,sans-serif;--font-mono:ui-monospace,Menlo,Consolas,monospace}` +
	`@media (prefers-color-scheme: light){:root{color-scheme:light;--background:oklch(0.97 0 0);--foreground:oklch(0.18 0 0);--muted:oklch(0.935 0 0);--muted-foreground:oklch(0.5 0 0);--card:oklch(0.975 0 0);--card-foreground:oklch(0.18 0 0);--popover:oklch(0.99 0 0);--border:oklch(0.91 0 0);--border-strong:oklch(0.86 0 0);--primary:oklch(0.24 0 0);--primary-foreground:oklch(0.97 0 0);--accent:oklch(0.24 0 0);--accent-foreground:oklch(0.97 0 0);--success:#16a34a;--warning:#c2412d;--destructive:oklch(0.577 0.245 27.325);--code:#304c83;--link:#304c83;--chart-1:#304c83;--chart-2:#0d9488;--chart-3:#d97706;--chart-4:#9333ea;--chart-5:#e11d48;--chart-6:#65a30d}}`

// The frame grows to fit the page, so a scrollbar inside the reply would read
// as a box within the thread; it stays hidden. The page's own CSS overrides all of this.
const renderBaseCSS = `html{background:var(--background);color:var(--foreground);font-family:var(--font-sans);font-size:14px;line-height:1.5;-webkit-font-smoothing:antialiased;scrollbar-width:none}html::-webkit-scrollbar{display:none}body{margin:0}code,kbd,pre,samp{font-family:var(--font-mono)}`

var (
	// A BOM, whitespace, and comments may precede the doctype. Anything inserted
	// before the doctype would drop the page into quirks mode.
	leadingDoctype = regexp.MustCompile(`(?is)^\x{FEFF}?(?:\s|<!--.*?-->)*<!doctype[^>]*>`)
	pageViewport   = regexp.MustCompile(`(?i)<meta\s[^>]*name\s*=\s*["']?viewport`)
)

// injectRenderBootstrap places the theme style and bootstrap script ahead of
// the page's own markup. Right after the doctype the HTML parser opens the
// implied <head> for them; the page's own <html> start tag then only merges
// its attributes and its <head> start tag is ignored, so the page's styles and
// scripts still come after the bootstrap. This needs neither an HTML tokenizer
// (golang.org/x/net is not a dependency) nor T3's backreference regex, which
// RE2 cannot express.
func injectRenderBootstrap(page string) string {
	var b strings.Builder
	if !pageViewport.MatchString(page) {
		b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	}
	b.WriteString(`<style id="ao-theme">` + renderDefaultThemeCSS + `</style>`)
	b.WriteString(`<style>` + renderBaseCSS + `</style>`)
	b.WriteString(`<script>` + renderBootstrapJS + `</script>`)
	if loc := leadingDoctype.FindStringIndex(page); loc != nil {
		return page[:loc[1]] + b.String() + page[loc[1]:]
	}
	return "<!doctype html>" + b.String() + page
}
