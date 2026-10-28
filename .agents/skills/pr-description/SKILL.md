---
name: pr-description
description: Write and maintain pull request descriptions in this repository, including the required change-count header, the before-and-after section, and screenshots or recordings for visible changes.
---

# PR description

Use the section order in `.github/pull_request_template.md`: change-count header,
What, Why, Before / After, How, Testing, Checklist. Preserve the rest of an existing
PR description when updating it.

## Change-count header

Begin every PR description with these three lines, using Markdown hard line breaks
so each renders separately:

```text
Code changes: +<added> -<deleted>
Tests: +<added> -<deleted>
Others: +<added> -<deleted>
```

Calculate the numbers from the net diff against the PR's actual base. For a local
branch, use `git diff --numstat <base>...<head>`. After pushing, prefer GitHub's
per-file additions and deletions. Recalculate after the final push rather than
summing commit churn or estimating counts.

Count every changed file exactly once:

- **Code changes:** handwritten implementation, including SQL, migrations, styles,
  and executable scripts.
- **Tests:** test code, fixtures, snapshots, and test-only helpers.
- **Others:** generated or vendored files, lockfiles, documentation, configuration,
  and assets. Generated output belongs here even when it contains source code.

Show `+0 -0` for empty categories. Binary files have no line count; mention their
count separately when present. Add a brief qualifier when useful, such as
`Others: +11 -0 (generated sqlc)`.

## Before / After

Every PR has a `## Before / After` section with a two-column table, one row per
behavior that changed:

```markdown
| Before | After |
| --- | --- |
| ![Before: empty model list](before.png) | ![After: list filtered to six models](after.png) |
| `ao session ls` exits 1 when a project has no sessions | Prints `No sessions` and exits 0 |
```

- **UI changes** need a screenshot of each changed state, before and after. Use a
  short recording for flows, animations, focus or keyboard behavior, and timing.
- **Non-visual changes** compare observable behavior: command output, API
  responses, errors, logs, or measured numbers with their units.
- **No user-visible change:** write `No user-visible change` and one line saying why,
  such as an internal refactor or a test-only change.

Capture both sides from the real app with the same screen, window size, and data,
so they compare directly. Take the before capture from the PR's base branch. The
`ao-desktop-dev` skill launches the desktop app from a checkout.

Screenshots and GIFs go in the table. GitHub renders a video player only for a
bare attachment URL on its own line, so put MP4 or MOV recordings on their own
line below the table, with one line saying what they show.

Never commit screenshots or recordings to the repository. Attach them in the PR
description, or in a PR comment when the description gets long. If you cannot
upload, give the capture file paths to the person who will attach them. Remove
secrets, tokens, and personal data from every capture.

## Verify

After creating or updating the PR, read the published description and verify that:

- its opening lines match the current GitHub diff counts and required order;
- every image and recording in Before / After, or in a PR comment, loads.
