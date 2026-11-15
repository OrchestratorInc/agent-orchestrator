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

const defaultThemeCSS = `:root{color-scheme:dark;--background:oklch(0.210 0.002 250);--foreground:oklch(0.970 0.002 250);--muted:oklch(0.295 0.002 250);--muted-foreground:oklch(0.720 0.002 250);--card:oklch(0.250 0.002 250);--card-foreground:oklch(0.970 0.002 250);--popover:oklch(0.300 0.002 250);--border:oklch(1 0 0 / 7%);--border-strong:oklch(1 0 0 / 4%);--primary:oklch(0.900 0.002 250);--primary-foreground:oklch(0.210 0.002 250);--accent:oklch(0.900 0.002 250);--accent-foreground:oklch(0.210 0.002 250);--success:#4ade80;--warning:#f06445;--destructive:oklch(0.704 0.191 22.216);--code:#79b0dc;--link:#79b0dc;--chart-1:#79b0dc;--chart-2:#2dd4bf;--chart-3:#fbbf24;--chart-4:#c084fc;--chart-5:#fb7185;--chart-6:#a3e635;--radius:0.5rem;--font-sans:ui-sans-serif,system-ui,sans-serif;--font-mono:ui-monospace,Menlo,Consolas,monospace}` +
	`@media (prefers-color-scheme: light){:root{color-scheme:light;--background:oklch(0.97 0 0);--foreground:oklch(0.18 0 0);--muted:oklch(0.935 0 0);--muted-foreground:oklch(0.5 0 0);--card:oklch(0.975 0 0);--card-foreground:oklch(0.18 0 0);--popover:oklch(0.99 0 0);--border:oklch(0.91 0 0);--border-strong:oklch(0.86 0 0);--primary:oklch(0.24 0 0);--primary-foreground:oklch(0.97 0 0);--accent:oklch(0.24 0 0);--accent-foreground:oklch(0.97 0 0);--success:#16a34a;--warning:#c2412d;--destructive:oklch(0.577 0.245 27.325);--code:#304c83;--link:#304c83;--chart-1:#304c83;--chart-2:#0d9488;--chart-3:#d97706;--chart-4:#9333ea;--chart-5:#e11d48;--chart-6:#65a30d}}`

const baseCSS = `html{background:var(--background);color:var(--foreground);font-family:var(--font-sans);font-size:14px;line-height:1.5;-webkit-font-smoothing:antialiased;scrollbar-width:none}html::-webkit-scrollbar{display:none}body{margin:0}code,kbd,pre,samp{font-family:var(--font-mono)}` +
	`:root{--viz-series-1:var(--chart-1);--viz-series-2:var(--chart-2);--viz-series-3:var(--chart-3);--viz-series-4:var(--chart-4);--viz-series-5:var(--chart-5);--viz-series-6:var(--chart-6);--viz-text:var(--foreground);--viz-muted:var(--muted-foreground);--viz-bg:var(--background);--viz-panel:var(--card);--viz-border:var(--border);--viz-accent:var(--accent);--viz-accent-text:var(--accent-foreground);--viz-accent-bg:var(--muted);--viz-warning:var(--warning);--blue:var(--chart-1);--green:var(--success);--orange:var(--warning);--yellow:var(--chart-3);--purple:var(--chart-4);--red:var(--destructive)}`

var (
	leadingDoctype      = regexp.MustCompile(`(?is)^\x{FEFF}?(?:\s|<!--.*?-->)*<!doctype[^>]*>`)
	pageViewport        = regexp.MustCompile(`(?i)<meta\s[^>]*name\s*=\s*["']?viewport`)
	unsafeFileNameChars = regexp.MustCompile(`[\\/:*?"<>|\p{Cc}]+`)
)

func FileName(title string) string {
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

var Version = func() string {
	sum := sha256.Sum256([]byte(inject("")))
	return hex.EncodeToString(sum[:])[:12]
}()

const (
	themeStyleOpen = `<style id="ao-theme">`
	viewportMeta   = `<meta name="viewport" content="width=device-width, initial-scale=1">`
)

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
