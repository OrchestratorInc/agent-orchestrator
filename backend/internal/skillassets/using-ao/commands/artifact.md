# ao artifact

Register files in the session's artifact directory as its deliverables.

## Syntax

```text
ao artifact add <path>...
```

A path is absolute, or relative to the artifact directory. Run from a folder
inside the artifact directory, a relative path is read from that folder when the
file is there. Put quotes around a path that contains spaces. It names a regular
file or a bundle directory (`.icon`, `.app`, `.bundle`, `.xcassets`), which is
listed as one artifact.

## What registering does

Only registered files are listed as the session's artifacts. Other files stay in
the directory, so a page's relative links to its images keep working, but they
are not listed. A session that registers nothing lists the files at the root of
its artifact directory instead.

Register each deliverable once, as soon as it exists:

```bash
ao artifact add report.html
```

- **Edits** need no command. AO records new versions on its own.
- **A renamed or moved file** stays registered and keeps its version history and share link. Rename it with `mv` in the artifact directory.
- **A copied file** is a new file, so run `ao artifact add` on the copy if it is a deliverable.
- **Supporting files**, such as a page's images, stay unregistered.

The command prints how many files stay unlisted and names the ones at the root
of the directory. If one of those is a deliverable, register it too.

`ao report --artifact` does not register a file. Neither does keeping a render
with `ao render --artifact` or `artifact: true` in `html_render`: run
`ao artifact add` on the path it returns.

## HTML artifacts on mobile

HTML deliverables may also be opened in the mobile app, where the available
width is typically about 390-440 CSS pixels. Make HTML artifacts responsive;
do not assume a desktop-width viewer.

- Include `<meta name="viewport" content="width=device-width, initial-scale=1">`.
- Use `box-sizing: border-box`, `max-width: 100%` for media, and avoid fixed
  widths or fixed multi-column layouts.
- Use shrinkable grid tracks (for example `minmax(0, 1fr)`), set
  `min-width: 0` on grid/flex children, and collapse dense columns to one
  column at narrow widths.
- Wrap long text and code. Keep wide tables in a horizontally scrollable
  wrapper so they do not force the whole page wider than the screen.
- Check the artifact at both desktop width (720px) and phone width (390px)
  before handing it over; fix horizontal overflow and clipped content.
