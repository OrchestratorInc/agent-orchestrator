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

When you produce a deliverable the user asked for that is not a PR (a requested
document or similar file), attach it as soon as it is ready, not only at
`--done`: use `--artifact <reference>` on the report for the milestone that
produced it. `--artifact` takes any opaque reference string; it is not validated
as a URL.
In a chat session, an HTML file from the artifact directory that you attach
with `--artifact` also shows in the thread.

Most tasks have no artifact. Findings, investigation results, verification, and
progress belong in the report note. Never attach screenshots, test logs, command
output, scratch notes, or intermediate diagnostics unless the user requested
them. One deliverable is one file.

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
