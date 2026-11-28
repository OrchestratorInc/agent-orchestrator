# Mobile: every pull request a session raised, from its PR chip

Status: design and mockup approved in conversation
Base: `main`. Deliberately not stacked on #8944 (see Approaches); one PR carries the Workers badge toning and the session chip and list.

## Intent

A worker often raises several pull requests. The mobile session screen shows at
most one of them: the composer's PR chip names the first PR that is still open
or a draft, and disappears once every PR is merged or closed. The others are
reachable only through the ⋯ menu, which, with several PRs, opens the app-wide
Pull Requests page rather than this worker's.

The session screen should show that there are several, colour each by its own
state, and let the user pick any of them and land on its review page.

## Goals

- The PR chip shows whenever the worker has any pull request, merged and closed
  included.
- With several PRs the chip says so (`+N`), in the colour of the PRs it counts.
- Tapping the chip with several PRs opens a list of all of them, floating over
  the composer the way #8944's permission picker does. Picking a row opens that
  PR's review page.
- A single PR still goes straight to its review page; no list.
- The ⋯ menu's "Pull requests" opens the same list when there are several.

## Non-goals

- No change to the review page itself, or to the app-wide Pull Requests page.
- No new daemon endpoint: the screen's existing PR summaries carry titles and
  status.
- No refactor of #8944's picker into a shared list (see Approaches).
- Desktop is unchanged.

## Anti-goals

- The list must not read as a choice of setting: nothing is ticked or "in
  force", unlike the permission picker. It is navigation.
- A worker whose PRs are all finished must not lose its chip (today's
  behaviour).
- Colour must not be the only signal: rows and the chip say their state in
  words to a screen reader.

## Approaches

1. **New `PullRequestPicker` on #8944's `ComposerOverlay`.** First chosen. Then
   dropped for its base: #8944 and #8934 branched before #8829 (the Workers
   badge), so one PR stacked on them cannot carry the badge change, and they
   are another contributor's branches. The user chose not to depend on #8944.
1b. **Chosen: the same list, built on main's own composer overlay pattern.**
   Main's effort slider floats `pillHeight` above the composer, fades in and
   out, closes on a touch outside, the keyboard or Android back, and turns the
   iOS back swipe off while up. The PR list does the same, with a copy of
   #8944's veil (about 30 lines of SVG) and #8944's row metrics. Cost: when
   #8944 lands, this list should move onto its `ComposerOverlay` and switch;
   whichever lands second reconciles them.
2. **Extract a shared `ComposerPickerList` from `PermissionPicker`.** No
   duplication, but it rewrites part of #8944 inside a PR stacked on it, so
   every #8944 change becomes a conflict. Better as a follow-up once #8944
   lands.
3. **A native sheet** like the account picker. Cheaper, but does not look like
   the floating list the user pointed at. Dropped.

## Decided

- Chip shows whenever the worker has any PR (option (a) in conversation).
- Base on main, with main's overlay pattern (approach 1b); not stacked on #8944.
- One PR: the chip and the ⋯ menu open its review directly.
- Several PRs, chip content: the lead PR (open first, then draft, merged,
  closed; newest within each, the Workers badge's order) as today's
  `#N · status`, then ` +N` in the other PRs' tone: red if any of them is
  closed, purple if all are merged, green if one is still open, quiet for
  drafts (the rule from the Workers badge branch).
- Several PRs, ⋯ menu → Pull requests: opens the same list instead of `/prs`.
- One PR for both: the Workers board badge's `+N` toning is the first commit
  of this branch (cherry-picked from the local `mobile-pr-pill-extra-tone`),
  and the session chip uses the same helper.

## Design

### Data

- `sessionPullRequests(session)`: every real PR (`number > 0`) from
  `session.prs`, or `session.pr`, in lead order. The first is the lead.
- The screen already reads PR summaries through `usePRSummaries`, but only when
  a reviewable PR exists. It now asks whenever the session has any PR, and the
  list looks up each row's summary with `summaryFor(config, sessionId, number)`.
- Row model, pure and unit tested:
  `{ number, title, lifecycle, glyph, glyphTone, status: { text, tone }, route }`
  - `title`: the summary's title, else `Pull request #N` until it loads.
  - `glyph` and `glyphTone`: the lifecycle's icon and tone, the same mapping as
    the Workers badge (open green, draft quiet, merged purple, closed red).
  - `status`: `prReviewPromptHeadline(pr, summary)` ("CI failing", "Ready to
    merge", "Merged", "Closed"…), in its own tone.
  - `route`: `reviewRouteForPR(sessionId, pr, hostId)`.

### Chip (`PRChip.tsx`, `ChatComposer.tsx`)

- Shown when `sessionPullRequests(session)` is non-empty, no longer only when
  `sessionPRReadyForReview` finds one.
- Content: today's `PRChipContent` for the lead, followed by ` +N` in the
  others' tone when there are more.
- Press: one PR, `onOpenReview(lead)`; several, opens the PR list.
- Screen reader: "Pull request 9137, CI passing, and 3 more merged" (or
  "and 3 more: 2 merged, 1 closed"); hint "Opens the pull request review" or
  "Lists this worker's pull requests".

### List (`pull-request-picker.tsx`, new)

- `PullRequestPickerOverlay`: like `EffortSliderOverlay`, positioned
  `pillHeight` above the composer pill, `FadeIn`/`FadeOut`, `markTouchInside`
  on touch start, iOS back swipe off while shown. Behind the list, a copy of
  #8944's arch veil (`pr-picker-veil`, radial gradient in `bgBase`) so the
  list reads over a busy conversation and covers the chips row.
- Open state: `usePullRequestPickerOpen(count, busy)`, modelled on
  `useEffortSliderOpen` but with no twelve-second timeout (it is a list to
  read). Closes on a touch outside, the keyboard showing, Android back, a pick,
  or when the session drops to fewer than two PRs.
- The composer owns both open states: opening the PR list closes the effort
  slider and the reverse.
- Rows: 58 pt, the icon column, then `#9137 Title` on the first line (number in
  the secondary colour, title in primary, one line, truncated) and the status
  headline on the second, in its tone. No selected wash: nothing is "in force".
  Pressed rows take `bgSubtle`.
- Width: 360 pt, capped to the composer.
- More than five PRs: the list scrolls inside a height of five and a half rows,
  so the half row says there is more.
- Order: lead order (open, draft, merged, closed; newest first within each).
- Pick: haptic select, close the list, `openPage(route)`.
- Accessibility: a list of buttons, not a radiogroup; each row's label is
  "Pull request 9137, Create brand and design guidelines doc, Mergeable".

### ⋯ menu (`ChatSessionScreen.tsx`)

- `pull_requests` with several PRs opens the list: the screen bumps a
  `pullRequestListRequest` counter prop on the composer (the `quoteRequest`
  pattern) once the actions sheet has closed, and the composer opens the list.

### Cloud sessions

`CloudSessionScreen` uses the same composer props (`reviewPR`, `onOpenReview`).
It gets the same chip and list, with `CLOUD_HOST_ID` routes.

## Testing

- Unit: `sessionPullRequests` order; row model titles, fallbacks, glyphs,
  tones and routes; the `+N` tone rule; accessibility labels.
- Component: chip shows for merged-only and closed-only workers; one PR opens
  the review; several open the list; a row press opens that PR's route; the
  list and the effort slider close each other; the ⋯ menu request opens it.
- Simulator: before/after on iOS (bundle swap) for one PR, several mixed, and
  all merged, dark and light; the list opened over the composer.

## Follow-up

When #8944 lands, move the list onto `ComposerOverlay` and its switch, and
drop the copied veil.

## Left to the builder

- Exact copy of the list's accessibility hint.
- Whether the row's number takes the lifecycle tone or the secondary text
  colour (mockup shows secondary; the icon carries the tone).
- Haptics beyond the pick.

## Shared helper

`workerRowPullRequest` (from the badge commit) already computes the lead PR,
`more`, `moreTone` and `moreLifecycles` in lead order. The session chip and the
list reuse it rather than a second ordering: the chip reads the lead and
`moreTone` from it, and `sessionPullRequests` returns the same lead-ordered
list the badge counts.
