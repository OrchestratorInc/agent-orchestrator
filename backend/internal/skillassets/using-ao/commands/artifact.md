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

Register each deliverable once, when it first exists:

```bash
ao artifact add report.html
```

- **Edits and overwrites** need no command. AO records new versions on its own.
- **A renamed or moved file** stays registered and keeps its version history and share link. Rename it with `mv` in the artifact directory.
- **A copied file** is a new file, so run `ao artifact add` on the copy if it is a deliverable.
- **Supporting files**, such as a page's images, stay unregistered.

## Iterating on a deliverable

A later round on the same thread or topic is a revision, not a new deliverable,
even when it covers a new detail. Do not write a new file and register it.

- Edit or overwrite the existing artifact. Rename it with `mv` to retitle it for
  the latest state, for example `mv draft-report.html q3-report.html`.
- For running before/after comparisons, keep one HTML page per thread, such as
  `design-polish.html`, register it once, and add each round to it. Its images
  are supporting files and stay unregistered.
- Review visuals nobody asked to keep go in the chat thread (`ao render` or
  `html_render`) or in the system temp directory, not in the artifact directory.

The command prints how many files stay unlisted and names the ones at the root
of the directory. If one of those is a deliverable, register it too.

`ao report --artifact` does not register a file. Neither does keeping a render
with `ao render --artifact` or `artifact: true` in `html_render`: run
`ao artifact add` on the path it returns.
