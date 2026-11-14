# ao artifact

Register files in the session's artifact directory as its deliverables.

## Syntax

```text
ao artifact add <path>...
```

A path is absolute, or relative to the artifact directory. It names a regular
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
