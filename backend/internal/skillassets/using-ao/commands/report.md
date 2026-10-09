# ao report

Persist a meaningful worker report for durable delivery to the active project
orchestrator.

## Syntax

```text
ao report <free-form-text>
ao report --checkpoint --note <text> [output flags]
ao report --needs-input --note <text> [output flags]
ao report --stuck --note <text> [output flags]
ao report --done --note <text> [output flags]
```

Output flags are repeatable:

```text
--artifact <opaque-reference>
--pr-created <pr-or-mr-url>
--pr-reviewed <pr-or-mr-url>
```

Use reports for meaningful transitions, decisions, blockers, required input,
outputs, and terminal judgment. Do not narrate routine commands. Outputs do not
imply completion, and `--done` does not terminate the session.

Attach a reviewable deliverable as soon as it is ready, not only at `--done`:
use `--artifact <reference>` on the report for the milestone that produced it.
Examples include a requested document, a rendered dashboard, or another output
the orchestrator or human needs to open directly. `--artifact` takes any opaque
reference string; it is not validated as a URL.

Routine test logs, command output, scratch notes, and intermediate diagnostics
are working material, not deliverables. Do not attach them unless requested or
needed to explain an actionable failure. Summarize validation in the report note;
when a separate deliverable is useful, prefer one consolidated report over
individual logs. Ordinary progress updates and concise final answers do not
need separate artifact files.

Saving a local artifact or attaching its reference to an AO report is not
external publishing authorization. Publish externally only within the
user-authorized scope.

`--pr-created` and `--pr-reviewed` accept complete GitHub PR or GitLab MR URLs.

`--needs-input` requests immediate non-interrupting delivery. `--stuck`
requests immediate delivery plus a rate-limited interrupt. Informational work
batches for up to one hour, while the first done report opens a fixed five
minute settlement window.

**Examples:**

```bash
ao report "The focused tests pass; I am checking the generated diff."
ao report --done --note "Ready for review." \
  --pr-created https://github.com/owner/repo/pull/88
```
