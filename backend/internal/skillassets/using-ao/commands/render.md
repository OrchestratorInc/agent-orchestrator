# ao render

Show a self-contained HTML page inline in the current **chat** session's
thread, above your final reply. Use it when a chart, table, diagram, image
collage, or mockup says more than prose.

```bash
ao render "$TMPDIR/turns-by-day.html" --title "Turns by day" --height 420
```

## Rules

- Call it before your final reply. The reader already sees the page, so the
  reply must not announce it, say where it is, or restate it. Add only what the
  page does not say.
- One self-contained HTML file: inline `<style>` and `<script>`, at most 1 MiB.
  Remote `https://` resources (a CDN chart library, for example) load as-is.
  Local paths and relative URLs do not resolve; embed images as `data:` URIs.
- Write the file outside the repository (for example under `$TMPDIR`) so it does
  not appear in the diff. AO stores its own copy.
- `--height` is the first-paint frame height (80-2000). The frame then fits the
  page's real height.
- Chat sessions only. In a terminal session, open the file with `ao preview`.

## ao render or the Browser panel

Use `ao render` when the page is the answer: a chart, table, diagram, or
mockup that you make from data you already have. The page stays in the thread.

Use `ao preview` and `ao browser` when the page is the work: an app that runs,
a page that needs a server or a login, or a page that you must click through.
The Browser panel shows the live page, and the next preview replaces it.

If the user must still read the page after the dev server stops, use `ao render`.

## Layout

- The frame is borderless on the thread background, as wide as the reply column,
  and its left edge lines up with your text.
- Use a fluid width with no outer padding, card, border, or banner title. The
  page is part of your reply.
- Give charts fixed pixel heights. Do not size `html` or `body` with `100vh` or
  `height: 100%`: the frame grows to fit the page, and viewport heights make it
  grow again.
- Scripts run in a sandbox with no access to AO, cookies, or storage. Links open
  in the user's browser.

## Theme

AO injects its active theme as CSS custom properties on `:root`. They follow
light/dark mode live:

`--background` (identical to the thread), `--foreground`, `--muted`,
`--muted-foreground`, `--card`, `--card-foreground`, `--popover`, `--border`,
`--border-strong`, `--primary`, `--primary-foreground`, `--accent`,
`--accent-foreground`, `--success`, `--warning`, `--destructive`, `--code`,
`--link`, `--chart-1` … `--chart-6` (categorical series), `--radius`,
`--font-sans`, `--font-mono`.

The base stylesheet sets the page background, text color, and font from these,
sets `body` margin to 0, and hides the page scrollbar. Use the variables rather
than hard-coded colors so the page reads correctly in both themes.

## Checking a page

If the file is inside the workspace and the Browser panel is not showing an app
the user is working with, `ao preview <path>` then `ao browser screenshot` shows
roughly how it looks (without the theme variables).
