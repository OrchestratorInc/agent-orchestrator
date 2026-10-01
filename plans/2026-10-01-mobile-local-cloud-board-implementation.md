# Mobile Local + Cloud Board Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show workers and projects from AO Cloud and one paired desktop together, with an explicit destination when spawning a worker.

**Architecture:** Keep Local and Cloud snapshots independent, attach a stable source identity to every displayed resource, and route reads/mutations through that identity. Replace the global view switch only after Workers, Projects, session/project routes, and spawn no longer depend on it. Preserve the existing daemon and Cloud APIs.

**Tech Stack:** Expo SDK 57, React Native, Expo Router, `@expo/ui`, TypeScript 6, Vitest 4.

**Spec:** `plans/2026-10-01-mobile-local-cloud-board-design.md`

## Global Constraints

- The only Local source is the active paired desktop; the only Cloud source is the signed-in organization's Cloud workspace. No multiple-host picker.
- No backend endpoints, daemon listener changes, credential handling changes, Cloud PR implementation, or production deployment.
- Existing Local-only mutations remain Local-only; Cloud remains open-only where it is today.
- Never add, stage, or commit files under `docs/`; preserve unrelated untracked files and stage exact paths only.
- Test iOS and Android. Run `npm --prefix packages/mobile run typecheck` and the full `npm --prefix packages/mobile test` suite before completion.

## Review Focus

- Both sources contain project/session ID `same`: distinct rows and route targets remain distinct (Tasks 1, 3, 4 tests).
- Cloud signs out or changes organization during a board request: old Cloud rows and late responses cannot enter the new account (Task 2 tests).
- The active paired desktop changes during a request: old Local rows and actions cannot be rebound to the new host (Tasks 2, 3 tests).
- One source fails while the other succeeds: the successful source remains visible and its actions work; no false empty state (Tasks 2, 4, 5 tests).
- The person changes spawn destination while catalog/model requests are in flight: the former source's options never appear and submission uses the current source (Task 6 tests).

---

## File map

- `packages/mobile/lib/environment/scopedBoard.ts`: source identity, resource keys, board composition, filter selectors. Pure TypeScript.
- `packages/mobile/lib/environment/scopedBoard.test.ts`: identity, composition, filtering, loading/error tests.
- `packages/mobile/lib/environment/resolve.ts`, `configLoad.ts`, `configLoadController.ts`, `shouldPoll.ts`, `boardSelection.ts`, `packages/mobile/lib/store.tsx`: independent Local/Cloud source resolution, polling, snapshots, refresh, and source-bound actions.
- `packages/mobile/lib/environment/{configLoadController,boardSelection,shouldPoll,store-wiring}.test.ts`, `store.source.test.ts`: source-generation and poll regression tests.
- `packages/mobile/lib/session/sessionRoute.ts`, `app/session/[id].tsx`, `app/project/[id].tsx`, `lib/useOrchestratorLauncher.ts`, `lib/chat/ChatSessionScreen.tsx`, `lib/session/CloudTerminalSessionScreen.tsx`, `lib/PushManager.tsx`, `lib/pushLifecycle.ts`: explicit source-aware route, chat/terminal, notification, and project actions.
- `packages/mobile/lib/worker-board-list.tsx`, `worker-list-row.tsx`, `worker-controls-sheet.tsx`, `worker-controls-sheet.android.tsx`, `worker-controls.ts`, `StaleBanner.tsx`, `app/(tabs)/index.tsx`: combined board, origin labels, Environment/project filters, per-row action policy.
- `packages/mobile/app/(tabs)/projects.tsx`, `prs.tsx`, `app/settings.tsx`, `app/create-project.tsx`, `app/onboarding.tsx`, `app/sheets/cloud-signin.tsx`, `lib/projects-view.ts`, `lib/prView.ts`, `lib/sidebar-navigation*.tsx`, `lib/sidebar-navigation.ts`: combined Projects, Local-only PR copy, Cloud import access, combined recents, removal of global picker.
- `packages/mobile/lib/spawn-composer-controls.{types,ios,android,tsx}`, `app/spawn.tsx`, `lib/spawnDestination.ts`: visible destination choice and safe state reset/submission.
- Relevant nearby `.test.ts(x)` files named in each task below. Do not modify generated code.

### Task 1: Source-qualified board contracts

**Files:** Create `packages/mobile/lib/environment/scopedBoard.ts` and `.test.ts`; leave existing UI unchanged.

**Interfaces:** Produce `SourceRef = { kind: EnvironmentKind; id: string }`, `Scoped<T> = { source: SourceRef; value: T }`, `sourceKey(source)`, `resourceKey(source,id)`, `composeBoards({local,cloud})`, `filterScopedWorkers(workers, environment, projectKey)`, and `resolveUnscopedId(id, entries)`. `environment` is `"all" | EnvironmentKind`; `projectKey` is `"all"` or `resourceKey(source,projectId)`. Later tasks consume these exact exports. Source IDs come from `machineIdentity(config)` and Cloud `orgId`, not display names.

- [ ] **Step 1: Write failing tests** for duplicate IDs, project lookup, environment/project filtering, and ambiguous unscoped routes. For example:

```ts
const local = { kind: "local", id: "mac-1" } as const;
const cloud = { kind: "cloud", id: "org-1" } as const;
const rows = [
  { source: local, value: { id: "same", projectId: "p" } },
  { source: cloud, value: { id: "same", projectId: "p" } },
] as Scoped<DashboardSession>[];
expect(resourceKey(local, "same")).not.toBe(resourceKey(cloud, "same"));
expect(resolveUnscopedId("same", rows)).toEqual({ kind: "ambiguous" });
expect(filterScopedWorkers(rows, "cloud", "all").every((row) => row.source.kind === "cloud")).toBe(true);
```

- [ ] **Step 2: Verify red:** `npm --prefix packages/mobile test -- lib/environment/scopedBoard.test.ts`. Expect missing exports/failed assertions, not a test setup error.
- [ ] **Step 3: Implement the pure contract.** `resourceKey` must encode each tuple element so separators in opaque IDs cannot collide; `composeBoards` must preserve source tags and per-source readiness rather than flattening errors into one flag. The core shape is:

```ts
export type SourceRef = Readonly<{ kind: EnvironmentKind; id: string }>;
export type Scoped<T> = Readonly<{ source: SourceRef; value: T }>;
export const sourceKey = (source: SourceRef) => JSON.stringify([source.kind, source.id]);
export const resourceKey = (source: SourceRef, id: string) => JSON.stringify([source.kind, source.id, id]);
export type SourceStatus = Readonly<{
  resolved: boolean; available: boolean; loading: boolean;
  stale: boolean; error: string | null;
}>;
export type SourceBoardInput = Readonly<{
  status: SourceStatus;
  snapshot?: { source: SourceRef; board: SessionSourceBoard };
}>;
export type ScopedBoard = {
  projects: Scoped<ProjectInfo>[];
  sessions: Scoped<DashboardSession>[];
  orchestrators: Scoped<OrchestratorLink>[];
  sources: { local?: SourceStatus; cloud?: SourceStatus };
};
export function composeBoards(input: { local: SourceBoardInput; cloud: SourceBoardInput }): ScopedBoard {
  const slices = [input.local.snapshot, input.cloud.snapshot].filter(
    (slice): slice is NonNullable<SourceBoardInput["snapshot"]> => !!slice);
  const collect = <T,>(pick: (board: SessionSourceBoard) => T[]): Scoped<T>[] =>
    slices.flatMap((slice) => pick(slice.board).map((value) => ({ source: slice.source, value })));
  return {
    projects: collect((board) => board.projects),
    sessions: collect((board) => board.sessions),
    orchestrators: collect((board) => board.orchestrators),
    sources: { local: input.local.status, cloud: input.cloud.status },
  };
}
export function filterScopedWorkers(
  workers: readonly Scoped<DashboardSession>[],
  environment: "all" | EnvironmentKind,
  projectKey: string,
): Scoped<DashboardSession>[] {
  return workers.filter(({ source, value }) =>
    (environment === "all" || source.kind === environment) &&
    (projectKey === "all" || resourceKey(source, value.projectId) === projectKey));
}
export function resolveUnscopedId<T extends { id: string }>(id: string, entries: readonly Scoped<T>[]) {
  const matches = entries.filter((entry) => entry.value.id === id);
  if (matches.length === 0) return { kind: "missing" as const };
  if (matches.length > 1) return { kind: "ambiguous" as const };
  return { kind: "found" as const, entry: matches[0] };
}
```

`SessionSourceBoard` is imported from `./board`. `composeBoards` always receives both statuses even when a source has not resolved an identity or snapshot yet; that is how the UI avoids a false empty state during startup. It does not erase a successful peer. `resolveUnscopedId` returns `{kind:"found",entry}`, `{kind:"missing"}`, or `{kind:"ambiguous"}`. Tests pin every branch.
- [ ] **Step 4: Verify green:** run the targeted test and mobile typecheck. Commit only the two new files with `feat(mobile): add source-qualified board contracts`.

### Task 2: Keep Local and Cloud boards live independently

**Files:** Modify `packages/mobile/lib/environment/resolve.ts`, `configLoad.ts`, `configLoadController.ts`, `shouldPoll.ts`, `boardSelection.ts`, `packages/mobile/lib/store.tsx`; extend `lib/environment/{configLoadController,shouldPoll,store-wiring,boardSelection}.test.ts` and create `packages/mobile/lib/store.source.test.ts` for pure-generation and source-action checks.

**Interfaces:** Consume Task 1's `SourceRef`/`ScopedBoard`; produce `acceptSourceResult({requested,current})`, `useApp().scopedBoard`, `useApp().localBoard`, `useApp().cloudBoard`, `useApp().sourceFor(source)`, `useApp().refreshSource(source)`, `useApp().refreshAll()`, `useApp().spawnOn(source, options)`, `useApp().launchConductorOn(source,projectId,clean?,mode?)`, and source-bound `killOn`, `renameWorkerOn`, `setWorkerPinnedOn`, `restoreOn`, `resumeAgentOn`. Keep `useApp().localPRs` derived only from Local sessions. Retain legacy `environment`, `setEnvironment`, and selected-board fields temporarily for untouched consumers; remove their UI only in Task 5. `sourceFor` returns a source only when its identity still matches the current paired machine or Cloud org/session epoch.

- [ ] **Step 1: Write failing tests** proving both poll gates can be active, source resolution does not invalidate its peer, and a stale Cloud or Local response is rejected. A key assertion is:

```ts
expect(shouldPollLocalSource({ paired: true, appActive: true })).toBe(true);
expect(shouldPollCloudSource({ signedIn: true, orgId: "org-1", appActive: true })).toBe(true);
const cloudAtEpoch1 = { source: { kind: "cloud", id: "org-1" }, generation: 1 } as const;
const cloudAtEpoch2 = { source: { kind: "cloud", id: "org-1" }, generation: 2 } as const;
const localMac1 = { source: { kind: "local", id: "mac-1" }, generation: 1 } as const;
const localMac2 = { source: { kind: "local", id: "mac-2" }, generation: 1 } as const;
expect(acceptSourceResult({ requested: cloudAtEpoch1, current: cloudAtEpoch2 })).toBe(false);
expect(acceptSourceResult({ requested: localMac1, current: localMac2 })).toBe(false);
```

- [ ] **Step 2: Verify red:** run `npm --prefix packages/mobile test -- lib/environment/configLoadController.test.ts lib/environment/shouldPoll.test.ts lib/store.source.test.ts`.
- [ ] **Step 3: Implement two resolved sources and independent request generations.** Replace the single module-wide `resolveSessionSource` cache with per-kind caches (or source-local `useMemo`s) so resolving Cloud cannot evict Local. Decouple the Local endpoint race/poll and Cloud board poll from `environment`; keep their existing app-active gating and intervals. When Local machine identity or Cloud org/session epoch changes, clear only that source's snapshot and invalidate in-flight requests. On a failed refresh, retain only that source's last good snapshot and mark it stale. The action boundary is:

```ts
type SourceRequest = { source: SourceRef; generation: number };
export const acceptSourceResult = ({ requested, current }: {
  requested: SourceRequest; current: SourceRequest;
}) => requested.generation === current.generation &&
  sourceKey(requested.source) === sourceKey(current.source);
const sourceFor = (ref: SourceRef): SessionSource | undefined =>
  ref.kind === "local" && ref.id === currentMachineId ? localSource :
  ref.kind === "cloud" && ref.id === currentOrgId ? cloudSource : undefined;
const spawnOn = async (ref: SourceRef, options: SpawnOptions) => {
  const source = sourceFor(ref);
  if (!source) throw new Error("This destination is no longer available.");
  return spawnSessionThroughSource(source, options, () => refreshSource(ref));
};
```

`refreshAll` uses `Promise.allSettled` for the available sources and exposes each error separately; it must not overwrite a good peer with an empty board. Replace the config-load controller's view-based `setEnvironment` gate with a Local-pairing readiness gate, so Cloud sign-in never suppresses desktop resolution. Source-bound Local mutations assert `ref.kind === "local" && ref.id === currentMachineId` before using the daemon config; Cloud cannot call them even if the legacy environment still says Local.
- [ ] **Step 4: Verify green:** run `npm --prefix packages/mobile test -- lib/environment/configLoadController.test.ts lib/environment/shouldPoll.test.ts lib/environment/store-wiring.test.ts lib/environment/boardSelection.test.ts lib/environment/board.test.ts lib/store.source.test.ts` and `npm --prefix packages/mobile run typecheck`. Commit exact touched files with `feat(mobile): maintain independent local and cloud boards`.

### Task 3: Route sessions and project actions by source

**Files:** Modify `packages/mobile/lib/session/sessionRoute.ts`, `packages/mobile/app/session/[id].tsx`, `packages/mobile/app/project/[id].tsx`, `packages/mobile/lib/useOrchestratorLauncher.ts`, `packages/mobile/lib/chat/ChatSessionScreen.tsx`, `packages/mobile/lib/session/CloudTerminalSessionScreen.tsx`, `packages/mobile/lib/PushManager.tsx`, `packages/mobile/lib/pushLifecycle.ts`. Extend `lib/session/sessionRoute.test.ts`, `lib/projectScreen.test.tsx`, `lib/useOrchestratorLauncher.test.tsx`, `lib/chat/ChatSessionScreen.source.test.ts`, `lib/session/CloudTerminalSessionScreen.source.test.ts`, and `lib/pushLifecycle.test.ts`.

**Interfaces:** Consume `SourceRef`, `resourceKey`, `useApp().scopedBoard/sourceFor/refreshSource`. Produce route params `{id, source: "local"|"cloud", sourceId}` for session and project routes. Export `routeSource(params)` which validates kind and identity; do not fall back from an invalid explicit source to the other source. Existing unscoped deep links use `resolveUnscopedId` only when unique.

- [ ] **Step 1: Write failing tests** for same-ID routes, unscoped ambiguity, stale paired-host route, Cloud/Local orchestrator dispatch, and a legacy Local push tap opening Workers (not a guessed session). For example:

```ts
expect(routeSource({ source: "cloud", sourceId: "org-1" })).toEqual({ kind: "cloud", id: "org-1" });
expect(routeSource({ source: "local", sourceId: "" })).toEqual({ kind: "invalid" });
expect(sessionDisplaySurface({ environment: "cloud", sessionMode: "tui", requestedView: "terminal" })).toBe("cloud-terminal");
```

- [ ] **Step 2: Verify red:** run `npm --prefix packages/mobile test -- lib/session/sessionRoute.test.ts lib/projectScreen.test.tsx lib/useOrchestratorLauncher.test.tsx lib/pushLifecycle.test.ts`. The new source route tests must fail because source params are not yet honored.
- [ ] **Step 3: Implement explicit routes.** Read route source before looking up `id`; select only that source's board and connection state. Key one-off Local session lookups by machine plus ID as today; Cloud list/lookup stays Cloud-only. Pass source into chat/terminal screens so their send, resume, and conversation source cannot follow global `environment`. A project page scopes project, sessions, and orchestrator to its route source. `useOrchestratorLauncher.openOrchestrator` accepts a `Scoped<OrchestratorProjectRow>` and dispatches by `row.source.kind`; its busy/idempotency map uses `resourceKey(row.source,row.value.project.id)`. The Local branch calls `launchConductorOn`, not the environment-implied `launchConductor`. Existing Local push payloads contain no host identity: their tap opens Workers rather than guessing a desktop session; do not attach the current machine ID to a possibly old notification. Source-qualified links do carry source identity. Every navigation from a known source includes `source` and `sourceId`:

```ts
router.push({
  pathname: "/session/[id]",
  params: { id, projectId, source: ref.kind, sourceId: ref.id },
});
```

For source loss, show Retry plus sign-in/pair action. For ambiguous legacy links, show a source choice or explanatory error; never guess based on `environment`.
- [ ] **Step 4: Verify green:** run `npm --prefix packages/mobile test -- lib/session/sessionRoute.test.ts lib/projectScreen.test.tsx lib/useOrchestratorLauncher.test.tsx lib/pushLifecycle.test.ts lib/chat/ChatSessionScreen.source.test.ts lib/session/CloudTerminalSessionScreen.source.test.ts` and `npm --prefix packages/mobile run typecheck`. Commit exact files with `feat(mobile): keep session and project routes source-bound`.

### Task 4: Combine Workers and sidebar recents

**Files:** Modify `packages/mobile/app/(tabs)/index.tsx`, `packages/mobile/lib/worker-board-list.tsx`, `worker-list-row.tsx`, `worker-controls.ts`, `worker-controls-sheet.tsx`, `worker-controls-sheet.android.tsx`, `StaleBanner.tsx`, `sidebar-navigation.ts`, `sidebar-navigation-shell.tsx`, `sidebar-navigation-shell.android.tsx`; extend their nearby tests.

**Interfaces:** Consume `scopedBoard`, `filterScopedWorkers`, `resourceKey`, Task 3's route params. Produce `WorkerControlsSheet` props `environmentFilter`, `onSelectEnvironment`, and source-qualified project options. `WorkerBoardList` accepts `Scoped<DashboardSession>[]` and chooses Local `full` or Cloud `open-only` interaction per row.

- [ ] **Step 1: Write failing tests** for All/Cloud/Local filtering, same-ID row keys, source-label accessibility, Local-only swipe/rename actions, Cloud open-only rows, one-source-error empty-state behavior, and sidebar routing. For example:

```ts
const rows = [
  { source: { kind: "local", id: "mac-1" }, value: { id: "same", projectId: "p" } },
  { source: { kind: "cloud", id: "org-1" }, value: { id: "same", projectId: "p" } },
] as Scoped<DashboardSession>[];
expect(filterScopedWorkers(rows, "all", "all")).toHaveLength(2);
expect(filterScopedWorkers(rows, "local", "all").map((r) => r.source.kind)).toEqual(["local"]);
expect(resourceKey(rows[0].source, rows[0].value.id)).not.toBe(resourceKey(rows[1].source, rows[1].value.id));
expect(sidebarSessionRoute(rows[1])).toMatchObject({ params: { source: "cloud", sourceId: "org-1" } });
```

- [ ] **Step 2: Verify red:** run `npm --prefix packages/mobile test -- lib/worker-controls.test.ts lib/worker-board-list.source.test.ts lib/sidebar-navigation.test.ts lib/sidebar-navigation-shell.source.test.ts`.
- [ ] **Step 3: Implement the combined board.** Remove board-level `interactionMode` as the sole policy: render each row with `entry.source.kind === "local" ? "full" : "open-only"`. Compute project labels by `resourceKey(source,projectId)`. Use `resourceKey` for FlatList rows, swipe state, and rename state. The filter sheet shows Environment before Projects and only projects allowed by that filter. All-project choices include source labels. The search string searches both sources. Display independent Local/Cloud notices with Retry; show “No workers” only after every configured source has completed an initial load. Update `StaleBanner` to accept a source status rather than the legacy active environment. Sidebar recents use the same entries and route params; keep source-specific health labels rather than one global “Disconnected.” The row policy must be selected from its own source:

```tsx
const rowKey = resourceKey(entry.source, entry.value.id);
<WorkerListRow
  key={rowKey}
  session={entry.value}
  source={entry.source}
  projectName={projectNames.get(resourceKey(entry.source, entry.value.projectId))}
  interactionMode={entry.source.kind === "local" ? "full" : "open-only"}
/>;
```
- [ ] **Step 4: Verify green:** rerun the Step 2 test command, then `npm --prefix packages/mobile run typecheck`. Commit exact files with `feat(mobile): show local and cloud workers together`.

### Task 5: Combine Projects and retire global environment controls

**Files:** Modify `packages/mobile/app/(tabs)/projects.tsx`, `prs.tsx`, `app/settings.tsx`, `app/create-project.tsx`, `app/onboarding.tsx`, `app/sheets/cloud-signin.tsx`, `lib/projects-view.ts`, `lib/prView.ts`, `lib/sidebar-navigation-shell.tsx`, `lib/sidebar-navigation-shell.android.tsx`; delete `lib/sidebar-environment-picker.tsx` only after removing all imports, and update its test. Extend `lib/projects-view.test.ts`, `lib/prView.test.ts`, `lib/project-card.source.test.ts`, `lib/sidebar-navigation-shell.source.test.ts`, `lib/cloud/createProjectScreen.test.tsx`.

**Interfaces:** Consume Task 2's independent boards and Task 3's source routes. Produce combined `Scoped<ProjectInfo>` project sections and a `projectRoute(entry: Scoped<ProjectInfo>)` helper returning `/project/[id]` with source params. Export `localOnlyPRCopy = "Pull requests from your paired desktop"` for the Local-only PR surface. The Cloud import entry point depends on Cloud auth, not selected `environment`. Settings retain Local pairing and Cloud account controls.

- [ ] **Step 1: Write failing tests** for same-name/same-ID projects from two sources, one source offline, Cloud import reachable without global switch, Local-only PR explanation, and no drawer/settings environment picker. For example:

```ts
const localProject = { source: { kind: "local", id: "mac-1" }, value: { id: "same", name: "P" } } as Scoped<ProjectInfo>;
const cloudProject = { source: { kind: "cloud", id: "org-1" }, value: { id: "same", name: "P" } } as Scoped<ProjectInfo>;
expect(resourceKey(localProject.source, localProject.value.id)).not.toBe(resourceKey(cloudProject.source, cloudProject.value.id));
expect(projectRoute(cloudProject).params).toMatchObject({ source: "cloud", sourceId: "org-1" });
expect(localOnlyPRCopy).toContain("paired desktop");
```

- [ ] **Step 2: Verify red:** run `npm --prefix packages/mobile test -- lib/projects-view.test.ts lib/prView.test.ts lib/project-card.source.test.ts lib/sidebar-navigation-shell.source.test.ts lib/settings-connection.source.test.ts lib/cloud/createProjectScreen.test.tsx lib/sidebar-environment-picker.test.ts`.
- [ ] **Step 3: Implement source-labeled Projects.** Compose sections from both source boards, key cards and orchestrator actions by source-qualified identity, and preserve each source's empty/loading/error state. Keep the Cloud GitHub import button visible when signed in and make `create-project.tsx` rely on Cloud auth rather than the legacy environment choice. Remove `setEnvironment("cloud")` from onboarding and the Cloud sign-in completion flow. Remove the drawer picker and Settings Environment row only after every visible screen no longer reads the global choice. Keep pairing and Cloud account controls. Make Pull Requests explicitly “From your paired desktop” and read `localPRs` even while Cloud is signed in. Remove or deprecate the persisted view choice only after its old consumers have migrated; do not use it to hide either board. Cards and actions carry their source:

```tsx
const key = resourceKey(entry.source, entry.value.id);
export const projectRoute = (entry: Scoped<ProjectInfo>) => ({
  pathname: "/project/[id]" as const,
  params: { id: entry.value.id, source: entry.source.kind, sourceId: entry.source.id },
});
const href = projectRoute(entry);
const label = entry.source.kind === "cloud" ? "Cloud" : `Local · ${desktopName}`;
export const localOnlyPRCopy = "Pull requests from your paired desktop";
```
- [ ] **Step 4: Verify green:** rerun the Step 2 test command after replacing the deleted picker test with a no-picker source test, then `npm --prefix packages/mobile run typecheck`. Commit exact files with `feat(mobile): combine projects and remove environment picker`.

### Task 6: Choose destination before spawning

**Files:** Create `packages/mobile/lib/spawnDestination.ts` and `.test.ts`; modify `packages/mobile/app/spawn.tsx`, `lib/spawn-composer-controls.types.ts`, `spawn-composer-controls.ios.tsx`, `spawn-composer-controls.android.tsx`, `spawn-composer-controls.tsx`; extend nearby spawn tests.

**Interfaces:** Consume Task 2's `sourceFor/spawnOn` and scoped projects. Produce `initialSpawnDestination(routeSource, availableSources)`, `canSubmitSpawn(destination,project,harness,sourceFor)`, and composer props `destinations`, `destination`, `onSelectDestination`. The destination option contains `source: SourceRef`, `label`, `available`, and an unavailable reason.

- [ ] **Step 1: Write failing tests** for project-prefill, generic `+` with two destinations requiring choice, one-destination auto-selection, destination-change reset, late catalog/model result rejection, and source-bound spawn response. For example:

```ts
expect(initialSpawnDestination(undefined, [local, cloud])).toBeNull();
expect(initialSpawnDestination(cloud, [local, cloud])).toEqual(cloud);
expect(initialSpawnDestination(undefined, [local])).toEqual(local);
expect(canSubmitSpawn(null, "project", "codex", () => undefined)).toBe(false);
```

- [ ] **Step 2: Verify red:** run `npm --prefix packages/mobile test -- lib/spawnDestination.test.ts lib/spawn.source.test.ts lib/spawn-composer-controls.ios.test.tsx lib/spawn-composer-controls.android.test.tsx`.
- [ ] **Step 3: Implement the **Run on** control above Project in all composer variants.** Use the existing `@expo/ui` SwiftUI Menu on iOS, Android's inline option-list pattern, and the universal `@expo/ui` Picker fallback; do not add another sheet. Increase the iOS controls host/stack height and adjust the keyboard-adjacent spacing so the new row does not clip or cover the submit button; keep Android's sheet scrollable. Selecting a destination resets destination-dependent state and increments a request generation. Local loads `getAgents/getSettings/getProject/getAgentModels`; Cloud loads Cloud provider connections. Async completions apply only for their captured generation:

```ts
const generation = ++catalogGeneration.current;
const result = await loadCatalog(destination);
if (generation !== catalogGeneration.current) return;
setCatalog(result);
```

Keep the prompt text when switching destination, but clear project/harness/model/mode/attachments and their errors. Project choices come only from the chosen source. Submit uses `spawnOn(destination, options)` and routes with `source`/`sourceId`; do not call the legacy environment-implied `spawn`. Keep the button disabled until destination, project, harness, and source are valid.
- [ ] **Step 4: Verify green:** run `npm --prefix packages/mobile test -- lib/spawnDestination.test.ts lib/spawn.source.test.ts lib/spawn-composer-controls.ios.test.tsx lib/spawn-composer-controls.android.test.tsx lib/session/sessionRoute.test.ts` and `npm --prefix packages/mobile run typecheck`. Commit exact files with `feat(mobile): choose cloud or paired desktop when spawning`.

### Task 7: Full regression and device review

**Files:** Only focused fixes and tests needed by failures in Tasks 1–6; no new feature scope.

**Interfaces:** No new interface. Confirm the combined screens satisfy the spec and both platforms.

- [ ] **Step 1: Run full static and unit validation.** Record exact failures and fix them before rerunning both commands:

```bash
npm --prefix packages/mobile run typecheck
npm --prefix packages/mobile test
git diff --check
```

- [ ] **Step 2: Review iOS Simulator and Android Emulator manually.** Use a paired dev desktop and a signed-in dev/staging Cloud account, not the user's production data. Verify: both sources visible; Environment and project filters; one source disconnected; Local and Cloud project detail; Cloud GitHub import; Local PR label; generic and project-prefilled spawns; source-bound worker conversation/terminal. Do not launch or overwrite a connected physical phone unless the user explicitly asks.
- [ ] **Step 3: Check branch hygiene and report.** `git status --short`, `git log -7 --oneline`, and `git diff 08d680c12..HEAD --name-only -- docs/` must show no `docs/` commits and no accidentally staged untracked directories. If a device or service cannot be used, report exactly what was not verified rather than claiming an end-to-end pass.
