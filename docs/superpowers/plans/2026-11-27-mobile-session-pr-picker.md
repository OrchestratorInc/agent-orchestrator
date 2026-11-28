# Mobile session PR chip and PR list Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The session screen's PR chip shows whenever a worker has any PR, says `+N` in the other PRs' tone, and with several PRs opens a floating list of all of them; a row opens that PR's review.

**Architecture:** One pure model module (`sessionPullRequests.ts`) builds the lead-ordered PRs and list rows from the badge helper already on this branch (`workerRowPullRequest.ts`). The composer renders the chip and hosts a new floating `PullRequestPicker` built on main's effort-slider overlay pattern. The local and cloud session screens feed it and route the ⋯ menu's "Pull requests" through it.

**Tech Stack:** React Native (Expo SDK 57), TypeScript, Reanimated, react-native-svg, vitest + react-test-renderer.

**Spec:** `docs/superpowers/specs/2026-11-27-mobile-session-pr-picker-design.md`

## Global Constraints

- Base is `main`; do not import anything from #8944 (`composer-overlay`, `permission-picker`, `effort-slider-veil`, `useEffortSliderSwitch`).
- Lifecycle tones: open `success`, draft `passive`, merged `merged`, closed `error` (`workerRowPullRequest.ts` `TONES`).
- Lead order: open, draft, merged, closed; newest (highest number) first within each.
- `+N` tone: red (`error`) if any other PR is closed, else green if one is open, else quiet (`passive`) if one is a draft, else purple (`merged`).
- List: row height 58, icon column 24, width 360 capped to the composer, scrolls past 5 rows inside 5.5 rows' height, no selected wash, pressed rows `t.bgSubtle`.
- Status line: `prReviewPromptHeadline(pr, summary)` text in its tone. Title fallback: `Pull request #N`.
- One PR: chip and ⋯ menu open its review directly. Several: they open the list.
- JS only: no `package.json`, `plugins/`, `app.config.js`, or native changes (keeps the change OTA-able).
- Commits: conventional, no attribution trailers.
- Run all commands from `packages/mobile`.

## Review Focus

1. Summaries not loaded yet (first open, offline): rows must show `Pull request #N` and a status from the PR row alone, not blank. Test in Task 1.
2. The session's PRs change while the list is open (a PR merged, or the worker drops to one PR): the list must update, and close when fewer than two remain. Test in Task 2.
3. Opening from the ⋯ menu: the touch that closes the actions sheet must not count as a touch outside and close the list at once. Test in Task 3.
4. Chip tapped with the keyboard up: the keyboard closes and the list stays open (the hook closes on `keyboardDidShow`, not hide). Test in Task 3.
5. Long titles and large text: every row stays one line per text, truncated, at any font scale. Test in Task 2.

---

### Task 1: Session PR model

**Files:**
- Modify: `lib/workerRowPullRequest.ts` (export the order, tones, glyphs and words it already has)
- Create: `lib/sessionPullRequests.ts`
- Test: `lib/sessionPullRequests.test.ts`

**Interfaces:**
- Consumes: `prLifecycle`, `prReviewPromptHeadline`, `Tone`, `PRLifecycle` from `./prView`; `reviewRouteForPR` from `./reviewView`.
- Produces:
  - `workerRowPullRequest.ts` exports `PR_TONES: Record<PRLifecycle, Tone>`, `PR_GLYPHS: Record<PRLifecycle, WorkerRowPullRequest["glyph"]>`, `leadOrdered(prs: readonly DashboardPR[]): DashboardPR[]`, `pullRequestMoreWords(pr: WorkerRowPullRequest): string` (the current private `moreWords`). `workerRowPullRequest` uses `leadOrdered(...)[0]` as its lead; its behaviour is unchanged.
  - `sessionPullRequests(session: Pick<DashboardSession, "pr" | "prs">): DashboardPR[]`: real PRs (`number > 0`), lead-ordered.
  - `type PullRequestRow = { pr: DashboardPR; number: number; title: string; lifecycle: PRLifecycle; glyph: WorkerRowPullRequest["glyph"]; glyphTone: Tone; status: { text: string; tone: Tone }; accessibilityLabel: string }`
  - `pullRequestRows(prs: readonly DashboardPR[], summaryFor: (number: number) => SessionPRSummary | undefined): PullRequestRow[]`
  - `type SessionPullRequests = { lead: DashboardPR; leadSummary?: SessionPRSummary; badge: WorkerRowPullRequest; rows: PullRequestRow[] }`
  - `sessionPullRequestModel(session: Pick<DashboardSession, "pr" | "prs">, summaryFor: (number: number) => SessionPRSummary | undefined): SessionPullRequests | undefined` (undefined with no PR).

- [ ] **Step 1: Write the failing tests** in `lib/sessionPullRequests.test.ts`:
  - `orders open, draft, merged, closed, newest first`: `sessionPullRequests({ prs: [pr(9001,"closed"), pr(9120,"merged"), pr(9137,"open"), pr(9125,"merged"), pr(9140,"open",true)] }).map(p => p.number)` equals `[9137, 9140, 9125, 9120, 9001]`; `{ pr: pr(5,"open") }` gives `[5]`; `{ prs: [pr(0,"open")] }` gives `[]`.
  - `builds a row per PR with its title, glyph, tones and words`: with `summaryFor(9137)` returning `{ title: "Create brand doc", ... }` and others undefined, row 0 equals `{ number: 9137, title: "Create brand doc", lifecycle: "open", glyph: "git-pull-request", glyphTone: "success", status: prReviewPromptHeadline(pr9137, summary), accessibilityLabel: "Pull request 9137, Create brand doc, <status text>" }`.
  - `falls back to the number before summaries load`: every row with no summary has `title: "Pull request #N"`; a merged row's status is `{ text: "Merged", tone: "merged" }`; a closed row's glyph is `"git-pull-request-closed"` with `glyphTone: "error"`.
  - `model is undefined with no PR and leads with the badge's lead`: `sessionPullRequestModel({}, () => undefined)` is undefined; for the five PRs above, `lead.number === 9137`, `badge.more === 4`, `badge.moreTone === "error"`, `rows.length === 5`.

- [ ] **Step 2: Run** `npx vitest run lib/sessionPullRequests.test.ts` — Expected: FAIL, module not found.

- [ ] **Step 3: Export the helpers from `lib/workerRowPullRequest.ts`** and implement `lib/sessionPullRequests.ts` with the signatures above. `leadOrdered` sorts a copy by `LEAD_ORDER` then number descending.

- [ ] **Step 4: Run** `npx vitest run lib/sessionPullRequests.test.ts lib/workerRowPullRequest.test.ts lib/worker-list-row.test.tsx` — Expected: PASS (badge tests unchanged).

- [ ] **Step 5: Commit** `feat(mobile): model every pull request a session raised`

### Task 2: PR picker list and its open hook

**Files:**
- Create: `lib/usePullRequestPicker.ts`, `lib/pull-request-picker.tsx`
- Test: `lib/usePullRequestPicker.test.tsx`, `lib/pull-request-picker.test.tsx`

**Interfaces:**
- Consumes: `PullRequestRow` (Task 1); `onTouchOutside`, `markTouchInside` from `./touchOutside`; `haptics.select`.
- Produces:
  - `usePullRequestPickerOpen(count: number, busy: boolean): { open: boolean; show(): void; toggle(): void; close(): void }`: modelled on `useEffortSliderOpen` (`lib/useEffortSlider.ts:16`) with no timeout; closes on a touch outside, `keyboardDidShow`, Android back, and whenever `count < 2` or `busy`.
  - `PullRequestPicker({ rows, onChoose }: { rows: readonly PullRequestRow[]; onChoose(pr: DashboardPR): void })`, `testID="pull-request-picker"`, rows `testID={"pull-request-" + number}`.
  - `PullRequestPickerOverlay({ bottom, rows, onChoose }: { bottom: DimensionValue; rows: readonly PullRequestRow[]; onChoose(pr: DashboardPR): void })`: as `EffortSliderOverlay` (`lib/effort-slider.tsx:43`): FadeIn/FadeOut, `onTouchStart={markTouchInside}`, iOS `gestureEnabled: false` while mounted, with an arch veil behind the list (copy of #8944's `effort-slider-veil.tsx` radial gradient, `VEIL_RISE` 220, as a private component in this file).

- [ ] **Step 1: Write the failing hook tests**:
  - `opens and closes on a touch outside`: `show()` → `open` true; firing the touch-outside listeners → false.
  - `has no timeout`: after `show()` and `vi.advanceTimersByTime(60_000)`, still open.
  - `closes when the keyboard shows and on back`: emit `keyboardDidShow` → closed; reopen, emit `hardwareBackPress` → closed and the handler returns true.
  - `closes when fewer than two remain` (Review Focus 2): open with `count` 3, rerender with `count` 1 → closed; `busy` true → closed.
- [ ] **Step 2: Write the failing list tests**:
  - `lists every row in order with title, number and status`: five rows render five `Pressable`s in order; row 0 texts include `"#9137"`, `"Create brand doc"`, its status text; the status `Text` style color is `toneColor(t, row.status.tone)`; the icon color is `toneColor(t, row.glyphTone)`.
  - `picks a row`: pressing `pull-request-9125` calls `onChoose` with that row's `pr` and `haptics.select` once.
  - `keeps every text on one line` (Review Focus 5): every title and status `Text` has `numberOfLines: 1`.
  - `scrolls past five rows`: with 7 rows the scroll container's `maxHeight` is `58 * 5.5`; with 3 rows nothing scrolls (`scrollEnabled` false).
  - `is a list of buttons, not a radio group`: no `accessibilityRole: "radio"`; each row `accessibilityRole: "button"`, label `row.accessibilityLabel`, hint `"Opens the pull request review"`.
- [ ] **Step 3: Run** `npx vitest run lib/usePullRequestPicker.test.tsx lib/pull-request-picker.test.tsx` — Expected: FAIL, modules not found.
- [ ] **Step 4: Implement** both files to the interfaces and Global Constraints' list metrics.
- [ ] **Step 5: Run** the same command — Expected: PASS.
- [ ] **Step 6: Commit** `feat(mobile): a floating list of a session's pull requests`

### Task 3: Composer chip and list wiring

**Files:**
- Modify: `lib/chat/PRChip.tsx`, `lib/chat/ChatComposer.tsx` (props at :106-161, chip at :760, effort at :618-621, overlay at :944)
- Test: `lib/chat/ChatComposer.pullRequests.test.tsx` (new, mocks as in `ChatComposer.quotes.test.tsx`)

**Interfaces:**
- Consumes: `SessionPullRequests`, `pullRequestMoreWords` (Task 1); `usePullRequestPickerOpen`, `PullRequestPickerOverlay` (Task 2).
- Produces: composer props replacing `reviewPR`, `reviewPRSummary`, `onOpenReview`:
  - `pullRequests?: SessionPullRequests`
  - `onOpenPullRequest?(pr: DashboardPR): void`
  - `pullRequestListRequest?: number` (a counter; each new value opens the list when it has two or more rows)
  - `PRChipContent({ pr, summary, more }: { pr; summary?; more?: { count: number; tone: Tone | null } })` draws ` +N` after the status in `toneColor(t, more.tone)` (secondary text colour when null).
  - `prChipAccessibilityLabel(pr, summary, badge?: WorkerRowPullRequest)` appends `pullRequestMoreWords(badge)`.

- [ ] **Step 1: Write the failing tests**:
  - `shows the chip for a worker whose PRs are all merged`: `pullRequests` from two merged PRs → chip present, text includes `"Merged"` and `" +1"` in `tone:merged`.
  - `one PR opens its review`: a single open PR → chip press calls `onOpenPullRequest` with it; no `pull-request-picker`.
  - `several PRs open the list, and a row opens its review`: three PRs → chip press shows `pull-request-picker`; pressing `pull-request-<n>` calls `onOpenPullRequest` with that PR and hides the picker.
  - `the list and the effort slider close each other`: open the list, toggle effort → picker gone; open effort, press the chip → effort overlay gone.
  - `a menu request opens the list after the sheet's touch` (Review Focus 3): render with `pullRequestListRequest={0}`, fire touch-outside listeners, rerender with `1` → picker shown; fire nothing else → still shown.
  - `pressing the chip with the keyboard up keeps the list open` (Review Focus 4): with the field focused, chip press calls `Keyboard.dismiss` and the picker is shown after a `keyboardDidHide` event.
  - `says the others to a screen reader`: label for open lead + 3 merged is `"Pull request 9137, <headline>, and 3 more merged"`; hint is `"Lists this worker's pull requests"` with several, `"Opens the pull request review"` with one.
- [ ] **Step 2: Run** `npx vitest run lib/chat/ChatComposer.pullRequests.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement**: chip shown whenever `pullRequests` is set; the PR picker's open state lives beside `effort`, each opener closing the other; measure `pillHeight` when either opens (`:619`, `:877`); render `PullRequestPickerOverlay` beside `EffortSliderOverlay` (`:944`); a `useEffect` on `pullRequestListRequest` calls `show()` (skip the initial value).
- [ ] **Step 4: Run** `npx vitest run lib/chat/` — Expected: PASS, including existing composer tests (update their props where they passed `reviewPR`).
- [ ] **Step 5: Commit** `feat(mobile): the session chip names every pull request and lists them`

### Task 4: Session screens and the ⋯ menu

**Files:**
- Modify: `lib/chat/ChatSessionScreen.tsx` (:384, :410-412, :724-732, :1064-1067), `lib/cloud/CloudSessionScreen.tsx` (:347-353, :401, :725-727)
- Modify: `lib/sessionPullRequests.ts` (add the menu helper)
- Test: `lib/sessionPullRequests.test.ts`

**Interfaces:**
- Consumes: Tasks 1 and 3.
- Produces: `pullRequestMenuAction(count: number): "review" | "list" | "none"`.

- [ ] **Step 1: Write the failing test** `menu opens the review for one PR and the list for several`: `pullRequestMenuAction(0)` is `"none"`, `(1)` `"review"`, `(4)` `"list"`.
- [ ] **Step 2: Run** `npx vitest run lib/sessionPullRequests.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** the helper, then in both screens:
  - fetch summaries whenever the session has any PR (`usePRSummaries([{ config, sessionId }])` locally, `useCloudPRSummaries([session.id])` in cloud), not only for `sessionPRReadyForReview`;
  - build `sessionPullRequestModel(session, n => summaryFor(...))` and pass `pullRequests`, `onOpenPullRequest={(pr) => openPage(reviewRouteForPR(session.id, pr, hostId))}`, `pullRequestListRequest`;
  - `pull_requests` menu action: `"review"` opens the lead's review, `"list"` increments the screen's `pullRequestListRequest` state; the local screen no longer opens `/prs`.
  - Orchestrator sessions (`"projectName" in session`) pass no `pullRequests`.
- [ ] **Step 4: Run** `npx tsc --noEmit -p . && npx vitest run` — Expected: clean typecheck, all tests pass.
- [ ] **Step 5: Commit** `feat(mobile): open a session's pull requests from its chip and menu`

### Task 5: Verify on the simulator and capture before/after

**Files:** none in the repo (lab route only in `/tmp` copies).

- [ ] **Step 1:** Bundle-swap main vs branch into the existing Release `.app` (`/tmp/ao448` recipe), with a lab route rendering the real `ChatComposer` (or session screen) for: one PR, all merged, mixed with a closed, and the list open.
- [ ] **Step 2:** Capture dark and light on an own iPhone 17 Pro (iOS 26.5) sim; check the list over the chips, the `+N` tones, tapping a row navigates, and the veil covers the chips.
- [ ] **Step 3:** Show the user before opening the PR. Delete the sim afterwards.
