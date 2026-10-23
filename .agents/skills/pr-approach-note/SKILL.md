---
name: PR approach note
description: Write a short, human-sounding note explaining the approach behind a PR or change, for a reviewer to read as a comment or description section.
---

# PR approach note

Write the note a good engineer would leave for a reviewer they respect: what they
saw, what they concluded, what they chose, and what still nags at them. The reader
has the diff open. They do not need it summarized; they need the reasoning that is
not in the diff.

## Before writing

Collect these from the branch itself:

- the exact symptom: error text, visible behaviour, which platform
- the mechanism you traced it to, with file and function
- what you tried and rejected, and why
- the honest cost of what you shipped
- what you verified, and what you could not verify on this machine
- anything a reviewer could reasonably disagree with

If one of those is missing, say so in the note rather than inventing it.

## Shape

Six things, in this order, as prose and not as a template:

1. **The symptom.** Start with what a user saw, not with the fix. Use the real
   string if there is one.
2. **The cause.** The mechanism, named precisely. Name the constant, function, or
   file that made the wrong decision.
3. **The change.** What you did and why that shape. If you moved a boundary, say
   which boundary.
4. **What you didn't do.** The rejected alternative and the reason. Reviewers learn
   the most from this part.
5. **The cost.** What got slower, bigger, riskier, or is still unhandled. Do not
   call a change free when it isn't.
6. **The evidence.** What you ran, what passed, what you could not run here, and how
   you know it addresses the reported bug.

150 to 350 words for a normal PR. Longer only when the tradeoff needs the room.

## Voice

- First person. "I traced it to", "I left", "I'm not sure".
- Past tense for work already done, present tense for what is true of the code.
- Concrete nouns: the function, the timeout, the platform. Not "the system",
  "the flow", "the layer".
- Short sentences next to longer ones. A one-line paragraph is fine.
- Admit uncertainty plainly. "I couldn't reproduce the Windows path locally, so
  there I only have the file-level reasoning" reads as honest. A hedge buried
  mid-sentence reads as evasion.
- No headings inside a short note. Headings make a comment look like a report.
  Use them only in a long PR description.

## Do not

- Open with "This PR ..." or restate the issue title.
- Narrate the diff file by file.
- Use robust, comprehensive, seamless, leverage, delve, straightforward,
  "it's worth noting", "this ensures", "the key insight", "best of both worlds".
- Join clauses with em dashes. Use a period or a comma.
- Use emoji, bold for emphasis, or a three-adjective bullet list.
- Thank the reviewer, celebrate the change, or call it elegant.
- Stack hedges. One honest caveat beats four.
- End with a summary paragraph that repeats the note.

## Example

Before, and this is what to avoid:

> This PR addresses an issue where OpenCode 2 model discovery would time out
> during the legacy data store migration. The implementation leverages a
> dedicated timeout budget to ensure comprehensive handling of the migration
> lifecycle, providing a robust and seamless experience for users.

After:

> Model discovery for OpenCode 2 failed at 30 seconds with `copy legacy data
> store: context deadline exceeded`, then deleted its staging directory and
> started the copy again on the next refresh. On a store big enough to need more
> than 30 seconds, it never finished at all.
>
> The copy only runs once, and readiness already gives it 30 minutes
> (`opencodeV2MigrationTimeout`). Discovery called the same `PrepareV2DataHome`
> under the generic 30-second budget, so it was abandoning a copy the rest of the
> daemon was still working on. I gave the discovery budget the migration timeout
> for `opencode-v2` only; every other harness still gets 30 seconds.
>
> I considered moving the migration out of discovery so no request ever waits on
> it. That is the better shape, but it changes how discovery finds the data home,
> and I did not want to do that inside a timeout fix. The cost of what I shipped
> is that one explicit refresh on the first run can now wait for the copy instead
> of failing at 30 seconds.
>
> The regression test fails on the old one-line budget and passes now. I ran the
> whole path on Windows against a fake `opencode.exe`, but the committed
> end-to-end test is POSIX-only, so that one has only run in CI.

## Delivering it

- PR comment: write the note to a file, then
  `gh pr comment <n> --body-file <file>`. No change-count header; that belongs to
  `pr-description`.
- PR body: keep `pr-description`'s three-line header first, then the note.
- Read it once before posting. If a sentence could have been written without
  reading the code, cut it.
