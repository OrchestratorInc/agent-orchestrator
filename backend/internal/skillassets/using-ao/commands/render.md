# ao render

Put an HTML page into the current **chat** session's thread, directly above
your final reply. Reach for it when a chart, table, diagram, image collage, or
mockup explains something better than prose would.

In a chat session, the `html_preview` and `html_render` tools do the same as
`ao render --check` and `ao render`. They work without shell access to the
daemon, so use them when you have them. The rules below apply to both.

```bash
ao render "$TMPDIR/turns-by-day.html" --title "Turns by day" --height 420
```

## Rules

- Run it before writing your final reply. The page is already on screen above
  the reply, so the reply neither introduces it, points to it, nor repeats it;
  it says only what the page leaves out.
- The page is one HTML file of at most 1 MiB with its CSS and JavaScript inline.
  It may load `https://` resources, such as a charting library from a CDN.
  Relative URLs do not resolve. Web workers do not run, and a page can reach no
  address on this computer or its local network.
- For a picture from this computer, name it by absolute path in an `img` `src`,
  a CSS `url()`, or a JavaScript string, for example `/tmp/plot.png`.
  `ao render` embeds it in the page. PNG, JPEG, GIF, WebP, AVIF, SVG, BMP, and ICO
  files work, each up to 10 MiB. Remote http(s) images load from the network.
- Write the file outside the repository (for example under `$TMPDIR`) so it does
  not appear in the diff. Orchestrator stores its own copy.
- Orchestrator measures the page when you publish it, so the frame opens at the right
  height. `--height` (80-2000) is used only when the desktop app is not running.
  The frame then fits the page's real height.
- Chat sessions only. In a terminal session, open the file with `ao preview`.
- Do not use a built-in visualize skill, for example the Codex visualize skill, or a
  `visualize{...}` line. Orchestrator does not show them. Only `ao render` or the `html_render` tool shows a page in the thread.

## Check before you publish

Run `ao render --check <file>` first. It opens the page in the Orchestrator desktop
app exactly as readers will get it and writes a PNG. It prints the page's content
height and its console messages. Read the PNG. Fix every `console.error` line. Use
`--width 390` to check a phone layout. The check loads only public addresses. A
request to this computer or to your local network fails. The check needs the
desktop app. Without it, publish without a check.

## ao render or the Browser panel

Use `ao render` when the page is the answer: a chart, table, diagram, or
mockup that you make from data you already have. The page stays in the thread.

Use `ao preview` and `ao browser` when the page is the work: an app that runs,
a page that needs a server or a login, or a page that you must click through.
The Browser panel shows the live page, and the next preview replaces it.

If the user must still read the page after the dev server stops, use `ao render`.

## ao render or a session artifact

Use `ao render` (or `html_render`) when the page answers a question in the thread.
Write a session artifact when the user asks for something to keep, for example a
report, a dashboard, or a document. Write it to the artifact directory that your
prompt names, run `ao artifact add <path>` so it is listed, and attach it with
`ao report --artifact <path>`.
In a chat session, an HTML file from the artifact directory that you attach
with `--artifact` shows in the thread as a row naming it, not as the page
itself; the reader opens it in the Artifacts tab. If the reader should read the
page in the thread, use `ao render`.

To keep a rendered page as an artifact as well, add `--artifact` to `ao render`
(or set `artifact: true` in `html_render`). Do this only when the user asks to keep
the page. Then run `ao artifact add` on the path it prints so the page is listed.

## Layout

- The page sits in the reply column with no frame border, over the same
  background as the thread, starting at the left edge of your text.
- Treat it as part of your answer: let it fill the width, and leave out outer
  padding, a wrapping card or border, and a heading banner.
- A reader can expand the page into a dialog of up to 1024 px wide, sized to its
  height. A page that fills the width uses that room. Orchestrator centers a
  top-level block that sets a `max-width`.
- Charts need a fixed height in pixels. The frame takes the page's height, so
  `html` and `body` must not be sized from the viewport (`100vh`,
  `height: 100%`), which would make the frame keep growing.
- Scripts are sandboxed away from Orchestrator, cookies, and storage. Links open
  in the user's browser.

## Theme

These CSS variables on `:root` hold Orchestrator's current theme and change when
the user switches between light and dark:

`--background` (the thread's own color), `--foreground`, `--muted`,
`--muted-foreground`, `--card`, `--card-foreground`, `--popover`, `--border`,
`--border-strong`, `--primary`, `--primary-foreground`, `--accent`,
`--accent-foreground`, `--success`, `--warning`, `--destructive`, `--code`,
`--link`, `--chart-1` … `--chart-6` (data series), `--radius`,
`--font-sans`, `--font-mono`.

A base stylesheet already applies the background, text color, and font, removes
the `body` margin, and hides the scrollbar in the thread. Color the page with the
variables instead of fixed values so it works in both themes.
