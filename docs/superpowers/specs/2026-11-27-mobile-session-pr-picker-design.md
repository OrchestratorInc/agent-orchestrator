# Mobile: every pull request a session raised, from its PR chip

Status: design approved in conversation, mockup pending review
Stacks on: #8944 (`feat/mobile-permission-picker`), itself on `fix/mobile-effort-slider`

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

1. **New `PullRequestPicker` on #8944's `ComposerOverlay`. Chosen.** Reuses the
   overlay (veil, reveal, outside-tap and back dismissal, iOS back-swipe guard)
   and the same open switch (`useEffortSliderSwitch`), so only one floating
   list is up at a time. Copies the row metrics. Leaves #8944's files alone, so
   the stack stays easy to review and survives changes to #8944. Cost: about 40
   lines of row styling duplicated.
2. **Extract a shared `ComposerPickerList` from `PermissionPicker`.** No
   duplication, but it rewrites part of #8944 inside a PR stacked on it, so
   every #8944 change becomes a conflict. Better as a follow-up once #8944
   lands.
3. **A native sheet** like the account picker. Cheaper, but does not look like
   the floating list the user pointed at. Dropped.

## Decided

- Chip shows whenever the worker has any PR (option (a) in conversation).
- Stack on #8944 and build on its overlay (approach 1).
- One PR: the chip and the ⋯ menu open its review directly.
- Several PRs, chip content: the lead PR (open first, then draft, merged,
  closed; newest within each, the Workers badge's order) as today's
  `#N · status`, then ` +N` in the other PRs' tone: red if any of them is
  closed, purple if all are merged, green if one is still open, quiet for
  drafts (the rule from the Workers badge branch).
- Several PRs, ⋯ menu → Pull requests: opens the same list instead of `/prs`.

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

- Hosted beside `EffortSliderHost` and `PermissionPickerHost` in the composer,
  on its own `useEffortSliderSwitch`, positioned by the same pill height. It
  floats over the chips row on the veil, like the permission list.
- Opening it closes the effort slider and permission picker, and opening either
  of those closes it (their buttons' `onPress` already close "whatever else is
  open"; the PR switch joins that set).
- Rows: 58 pt, the icon column, then `#9137 Title` on the first line (number in
  the secondary colour, title in primary, one line, truncated) and the status
  headline on the second, in its tone. No selected wash: nothing is "in force".
  Pressed rows take `bgSubtle`.
- Width: 360 pt like the permission list, capped to the composer.
- More than five PRs: the list scrolls inside a height of five and a half rows,
  so the half row says there is more.
- Order: lead order (open, draft, merged, closed; newest first within each).
- Pick: haptic select, close the list, `openPage(route)`.
- Accessibility: the list is a `menu`/list of buttons, not a radiogroup; each
  row's label is "Pull request 9137, Create brand and design guidelines doc,
  CI passing".

### ⋯ menu (`ChatSessionScreen.tsx`)

- `pull_requests` with several PRs opens the list: the PR switch moves up to
  the screen and is passed to the composer, so the menu action can call
  `setOpen(true)` once the actions sheet has closed.

### Cloud sessions

`CloudSessionScreen` uses the same composer props (`reviewPR`, `onOpenReview`).
It gets the same chip and list, with `CLOUD_HOST_ID` routes.

## Testing

- Unit: `sessionPullRequests` order; row model titles, fallbacks, glyphs,
  tones and routes; the `+N` tone rule; accessibility labels.
- Component: chip shows for merged-only and closed-only workers; one PR opens
  the review; several open the list; a row press opens that PR's route; the
  list closes the effort slider and permission picker and vice versa.
- Simulator: before/after on iOS (bundle swap) for one PR, several mixed, and
  all merged, dark and light; the list opened over the composer.

## Left to the builder

- Exact copy of the list's accessibility hint.
- Whether the row's number takes the lifecycle tone or the secondary text
  colour (mockup shows secondary; the icon carries the tone).
- Haptics beyond the pick.

## Open

- The `+N` tone rule lives on the Workers badge branch
  (`mobile-pr-pill-extra-tone`, not yet a PR). This stack needs the same
  helper; see the hand-off question.
