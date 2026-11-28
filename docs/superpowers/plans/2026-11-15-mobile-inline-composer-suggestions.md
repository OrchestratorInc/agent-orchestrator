# Mobile inline composer suggestions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `/`/`@` composer picker sheet with an inline list docked above the mobile chat composer.

**Architecture:** `ChatComposer` already derives the active trigger from `text` + `cursor` (`findComposerSuggestion`). Instead of pushing a sheet route, it loads the catalog into local state and renders a presentational `ComposerSuggestionPanel` above the composer row inside the keyboard-riding dock.

**Tech Stack:** React Native 0.86 / Expo, vitest + react-test-renderer.

**Spec:** `docs/superpowers/specs/2026-11-15-mobile-inline-composer-suggestions-design.md`

## Global Constraints

- The TextInput keeps focus; the list uses `keyboardShouldPersistTaps="handled"`.
- One elevated surface, hairline dividers between rows, no per-row borders, no helper copy (DESIGN.md §9, §13).
- Ranking and insertion reuse `rankComposerCatalog` / `replaceComposerSuggestion` unchanged.

## Review Focus

- A slow catalog load resolving after the trigger was deleted must not show the panel (existing `pickerGate`).
- Typing `/` again after a selection (`/foo /`) is not a trigger (slash only at message start) — panel stays hidden.
- Selecting a row closes the panel even though the caret then sits right after the inserted token + space.
- Switching sessions / clearing text after send hides the panel.
- An empty catalog shows "No matching skills" rather than an empty box.

---

### Task 1: `ComposerSuggestionPanel`

**Files:**
- Create: `packages/mobile/lib/chat/ComposerSuggestionPanel.tsx`
- Test: `packages/mobile/lib/chat/ChatComposer.suggestions.test.tsx` (via Task 2)

**Interfaces:**
- Produces: `ComposerSuggestionPanel({ kind: "skills" | "files"; items: RankedSuggestion[] | undefined /* undefined = loading */; truncated?: boolean; onSelect(value: string): void })`

- [ ] Implement: FlatList, max height 232, rows ≥ 52 pt; skills label rendered as `/${label}`; detail one line; badge right; loading = ActivityIndicator row; empty = "No matching skills" / "No matching files"; truncated files → one amber line.

### Task 2: Wire into `ChatComposer`, delete the sheet

**Files:**
- Modify: `packages/mobile/lib/chat/ChatComposer.tsx:432-463` (openPicker/useEffect) and the dock contents
- Delete: `lib/chat/ComposerPickerSheet.tsx`, `lib/chat/ComposerPickerSheet.test.ts`, `app/sheets/composer-picker.tsx`, `composerSheetContentStyle`
- Modify: `chatSheetRegistry.ts(+test)`, `app/_layout.tsx:44`, `RouteErrorBoundary.test.ts`, `interface-affordances.source.test.ts:92`
- Test: `packages/mobile/lib/chat/ChatComposer.suggestions.test.tsx`

- [ ] Write failing tests: typing `/` shows a row `/approve` from `onLoadSkills`; typing `/ap` filters out `/background`; pressing a row sets the input to `/approve ` and hides the panel; typing `hello /` shows no panel; `@` lists files.
- [ ] Run `npx vitest run lib/chat/ChatComposer.suggestions.test.tsx` → FAIL.
- [ ] Implement: `catalog` state keyed by kind, loaded once per trigger open through `pickerGate`; `dismissedKey` ref so a selection closes the panel; render panel above the composer when a trigger is active.
- [ ] Run the full mobile suite + `npx tsc --noEmit` → PASS.
- [ ] Commit `feat(mobile): show / and @ suggestions inline above the composer`.

### Task 3: Show it

- [ ] Run on the iOS simulator against a scratch daemon, type `/`, screenshot.
