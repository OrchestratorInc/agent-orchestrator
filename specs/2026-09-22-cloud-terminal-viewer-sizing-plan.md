# Cloud Terminal Viewer Sizing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep a Cloud terminal full-sized on its visible desktop while mobile mirrors it with zoom/pan, and hand sizing to mobile when desktop parks that session.

**Architecture:** An opt-in protocol-3 WebSocket carries each viewer's role, visibility, and natural fit. Postgres stores short-lived viewers and one elected grid per terminal; all control-plane replicas read that grid, and only a changed election queues a worker PTY resize. Desktop publishes visibility independently of xterm resize; mobile proposes its fit but renders the server's grid with the Local terminal's viewport controls.

**Tech Stack:** Go, pgx/Postgres, coder/websocket, React/Electron, React Native/Expo, xterm WebView, Vitest.

**Spec:** `specs/2026-09-22-cloud-terminal-viewer-sizing.md`

## Global Constraints

- Work on the current `mobile/work-ahead` branch; preserve all pre-existing dirty files and stage only task-owned paths for any commit.
- Do not edit or commit anything under `docs/`.
- Do not deploy the control plane or publish desktop/mobile as a validation step.
- Keep protocol 2 wire behavior unchanged during rollout; new clients opt into protocol 3.
- Local daemon terminal arbitration and Local mobile sizing must not change behavior.
- One Cloud terminal means one PTY and one authoritative grid, never independent simultaneous TUI layouts.

## File map and interfaces

- `cloud/internal/terminalview/authority.go`: pure election of a whole `(columns, rows)` pair from visible viewers.
- `cloud/internal/postgres/migrations/00040_terminal_viewers.sql`: viewer leases and authoritative grid columns, with RLS and reversible Down migration.
- `cloud/internal/postgres/terminal_view_store.go`: serialized viewer upsert/removal/expiry, durable election and resize request, grid read.
- `cloud/internal/httpapi/terminal_handlers.go` and `terminal_stream.go`: protocol-3 attach/heartbeat/output; `server.go` adds store methods; `cloud/cmd/ao-cloud/main.go` registers cross-replica size notifications if used.
- `frontend/src/renderer/lib/cloud-terminal-mux.ts` and `terminal-mux.ts`: protocol-3 frames and an optional Cloud-only viewer-state method; `frontend/src/renderer/hooks/useTerminalSession.ts`: park/reactivate signals.
- `packages/mobile/lib/cloud/terminal.ts` and `packages/mobile/lib/session/CloudTerminalSessionScreen.tsx`: secondary viewer proposal, authoritative grid rendering, zoom controls.
- `packages/mobile/lib/session/terminalEnhanceScript.ts`: extract the Local terminal's existing touch/zoom WebView script for use by both Local and Cloud screens without changing the Local behavior.

### Task 1: Pure grid election

**Files:** Create `cloud/internal/terminalview/authority.go`, `cloud/internal/terminalview/authority_test.go`.

**Interfaces:** Produce `type Viewer { Role string; Visible bool; Columns, Rows uint16 }`, `type Grid { Columns, Rows uint16 }`, and `func Elect([]Viewer) Grid`. A zero grid means no eligible viewer; callers then retain the last authoritative grid.

- [ ] **Step 1: Write the failing table test.** Include desktop 120×40 versus mobile 55×39, parked desktop fallback, two primaries where the larger whole pair wins, all hidden, and zero dimensions. The core assertions are:

```go
tests := []struct { name string; viewers []Viewer; want Grid }{
    {"desktop wins", []Viewer{{Role:"primary", Visible:true, Columns:120, Rows:40}, {Role:"secondary", Visible:true, Columns:55, Rows:39}}, Grid{120,40}},
    {"parked desktop", []Viewer{{Role:"primary", Visible:false, Columns:120, Rows:40}, {Role:"secondary", Visible:true, Columns:55, Rows:39}}, Grid{55,39}},
    {"none measured", []Viewer{{Role:"secondary", Visible:true}}, Grid{}},
}
for _, tt := range tests { t.Run(tt.name, func(t *testing.T) { if got := Elect(tt.viewers); got != tt.want { t.Fatalf("got %+v; want %+v", got, tt.want) } }) }
```

- [ ] **Step 2: Run the red test.** Run `cd cloud && go test ./internal/terminalview`; expect undefined `Viewer`/`Elect`.
- [ ] **Step 3: Implement the selector.** First determine whether any valid visible primary exists. Then select the eligible viewer with the greatest `int(Columns)*int(Rows)` area. Never combine one viewer's columns with another's rows:

```go
func Elect(viewers []Viewer) Grid {
    hasPrimary := false
    for _, v := range viewers { if v.Visible && v.Role == "primary" && v.Columns > 0 && v.Rows > 0 { hasPrimary = true } }
    best, area := Grid{}, 0
    for _, v := range viewers {
        if !v.Visible || v.Columns == 0 || v.Rows == 0 || (hasPrimary && v.Role != "primary") { continue }
        if next := int(v.Columns) * int(v.Rows); next > area { area, best = next, Grid{Columns:v.Columns, Rows:v.Rows} }
    }
    return best
}
```

- [ ] **Step 4: Run `cd cloud && go test ./internal/terminalview` and `go vet ./internal/terminalview`; expect pass.**
- [ ] **Step 5: Commit only these two files:** `git add cloud/internal/terminalview/authority.go cloud/internal/terminalview/authority_test.go && git commit -m 'feat(cloud): elect terminal viewer grid'`.

### Task 2: Durable, cross-replica viewer state

**Files:** Create `cloud/internal/postgres/migrations/00040_terminal_viewers.sql`, `cloud/internal/postgres/terminal_view_store.go`; modify `cloud/internal/httpapi/server.go`. Test in `cloud/internal/httpapi/terminal_view_store_test.go` with a narrow fake Store, plus Cloud local Postgres smoke for actual SQL.

**Interfaces:** Consume `terminalview.Viewer/Grid`. Produce store methods `UpsertTerminalViewer(ctx context.Context, terminal domain.TerminalSession, viewerID string, viewer terminalview.Viewer, lease time.Duration) (terminalview.Grid, error)`, `RemoveTerminalViewer(ctx, terminal, viewerID) (terminalview.Grid, error)`, and `TerminalGrid(ctx, terminal) (terminalview.Grid, error)`. `viewerID` is a server-generated UUID scoped to one WebSocket. `Upsert` accepts zero fit but rejects unknown roles; the handler validates the wire before calling it.

- [ ] **Step 1: Add failing store-contract tests via a fake `Store`.** Drive an in-memory fake through the protocol-facing method signatures and assert that an unchanged proposal produces no second resize, parked desktop elects mobile, detach removes its viewer, and expiry of an unrefreshed desktop lets a refreshing mobile win. Use `terminalview.Elect` in the fake so the test isolates the contract; the Cloud-local smoke below verifies the SQL implementation. A representative assertion is:

```go
desktop := terminalview.Viewer{Role:"primary", Visible:true, Columns:120, Rows:40}
phone := terminalview.Viewer{Role:"secondary", Visible:true, Columns:55, Rows:39}
grid, err := store.UpsertTerminalViewer(ctx, terminal, "desktop-id", desktop, time.Minute)
if err != nil || grid != (terminalview.Grid{Columns:120, Rows:40}) { t.Fatalf("desktop grid=%+v err=%v", grid, err) }
grid, err = store.UpsertTerminalViewer(ctx, terminal, "phone-id", phone, time.Minute)
if err != nil || grid.Columns != 120 { t.Fatalf("phone shrank desktop: %+v err=%v", grid, err) }
```
- [ ] **Step 2: Run `cd cloud && go test ./internal/httpapi -run TerminalViewer`; expect compile failure for missing Store methods.**
- [ ] **Step 3: Add the migration.** Add `authoritative_columns`, `authoritative_rows`, and `authoritative_revision` to `ao_terminal_sessions` as nonnegative integer/integer/bigint with zero defaults. Create `ao_terminal_viewers` keyed by viewer UUID, scoped by `(org_id, session_id, terminal_id)` FK to `ao_terminal_sessions`, with `role IN ('primary','secondary')`, `visible`, integer columns/rows constrained to 0–65535, `expires_at`, and an index on `(terminal_id, expires_at)`. Enable and force RLS with the existing `ao_current_org_id()` tenant policy. Down drops the viewer table, then added columns. The table must not alter an already-merged migration:

```sql
-- +goose Up
ALTER TABLE ao_terminal_sessions ADD COLUMN authoritative_columns INTEGER NOT NULL DEFAULT 0 CHECK (authoritative_columns BETWEEN 0 AND 65535);
ALTER TABLE ao_terminal_sessions ADD COLUMN authoritative_rows INTEGER NOT NULL DEFAULT 0 CHECK (authoritative_rows BETWEEN 0 AND 65535);
ALTER TABLE ao_terminal_sessions ADD COLUMN authoritative_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE ao_terminal_viewers (
  id UUID PRIMARY KEY, org_id UUID NOT NULL, session_id UUID NOT NULL, terminal_id UUID NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('primary','secondary')), visible BOOLEAN NOT NULL,
  columns INTEGER NOT NULL CHECK (columns BETWEEN 0 AND 65535),
  rows INTEGER NOT NULL CHECK (rows BETWEEN 0 AND 65535), expires_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (org_id,session_id,terminal_id) REFERENCES ao_terminal_sessions(org_id,session_id,id) ON DELETE CASCADE
);
CREATE INDEX ao_terminal_viewers_active_idx ON ao_terminal_viewers(terminal_id,expires_at);
ALTER TABLE ao_terminal_viewers ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_terminal_viewers FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_terminal_viewers_tenant_policy ON ao_terminal_viewers USING (org_id=ao_current_org_id()) WITH CHECK (org_id=ao_current_org_id());
-- +goose Down
DROP TABLE ao_terminal_viewers;
ALTER TABLE ao_terminal_sessions DROP COLUMN authoritative_revision, DROP COLUMN authoritative_rows, DROP COLUMN authoritative_columns;
```

- [ ] **Step 4: Implement one serialized reconciliation transaction.** Use `withOrg`; `SELECT ... FROM ao_terminal_sessions ... FOR UPDATE` fenced by org/session/terminal/worker epoch; delete expired viewer rows for that terminal; upsert/remove the socket viewer; read live viewers; call `terminalview.Elect`; retain the stored grid if no eligible viewer. When elected grid differs, call existing `createWorkerRequest(ctx, tx, orgID, sessionID, "terminal.resize", payload, 15*time.Second, "")` in the SAME transaction, then update grid/revision and `pg_notify('ao_terminal_size', terminal.ID)`. If worker is temporarily absent or queue is full, rollback and return `ErrWorkerUnavailable`/`ErrConflict`; handler retries on a later heartbeat, so it cannot publish an unqueued grid. On an unchanged grid, inspect the newest resize request for that terminal and exact grid: if it failed or expired rather than completed/pending/claimed, enqueue a fresh request on the lease refresh. This makes a worker-side resize failure retryable without churning successful grids. `TerminalGrid` reads the committed grid under org RLS. The essential changed-grid update is:

```go
if elected != (terminalview.Grid{}) && elected != current {
    payload, _ := json.Marshal(map[string]any{"terminalId":terminal.ID, "columns":elected.Columns, "rows":elected.Rows})
    if _, err := createWorkerRequest(ctx, tx, terminal.OrgID, terminal.SessionID, "terminal.resize", payload, 15*time.Second, ""); err != nil { return err }
    _, err := tx.Exec(ctx, `UPDATE ao_terminal_sessions SET authoritative_columns=$1,authoritative_rows=$2,authoritative_revision=authoritative_revision+1 WHERE org_id=$3 AND session_id=$4 AND id=$5`, elected.Columns,elected.Rows,terminal.OrgID,terminal.SessionID,terminal.ID)
    if err != nil { return err }
    _, err = tx.Exec(ctx, `SELECT pg_notify('ao_terminal_size',$1)`, terminal.ID)
    if err != nil { return err }
}
```

- [ ] **Step 5: Run `cd cloud && go test ./internal/postgres ./internal/httpapi -run TerminalViewer` and `go vet ./internal/postgres ./internal/httpapi`; expect pass.** Start the existing Cloud-local test database only if its scratch-data prerequisites are available; run migration Up/Down against that scratch database and the `cloud/scripts/test-cloud-local.sh` smoke, never against production. In that scratch database, attach a viewer to epoch 1, replace the terminal with epoch 2, and verify the old viewer cannot update the new terminal's grid. If Docker or credentials are unavailable, record this as an unverified integration gate rather than claiming SQL passed.
- [ ] **Step 6: Commit only task files** with `feat(cloud): persist terminal viewer sizing`; no `docs/` files.

### Task 3: Protocol-3 attach, size push, and lease refresh

**Files:** Modify `cloud/internal/httpapi/terminal_handlers.go`, `cloud/internal/httpapi/terminal_stream.go`, `cloud/cmd/ao-cloud/main.go`; test in `cloud/internal/httpapi/terminal_stream_test.go` and a new `cloud/internal/httpapi/terminal_view_protocol_test.go`.

**Interfaces:** Consume the three Task-2 store methods. Client frame is `{type:"viewer",role:"primary"|"secondary",visible:boolean,columns:number,rows:number}`; server frame is `{type:"size",columns:number,rows:number}`. A protocol-3 socket creates its own UUID and removes that viewer at disconnect; protocol-2 routes continue to call `QueueTerminalResize` as before.

- [ ] **Step 1: Write failing WebSocket tests.** With `httptest` and a fake `Store`, assert protocol 3 rejects an initial input/resize or malformed role with `StatusProtocolError`; accepts a zero-size secondary initial viewer without electing it; sends `size` before any replayed `output` once a nonzero grid exists; delivers a changed grid after a second fake replica updates shared state; and removes its viewer on close. Keep the existing protocol-2 relay test unchanged. Use a fake store embedding `Store` and overriding only viewer/grid/output methods. The ordering assertion reads frames until the first output:

```go
seenSize := false
for {
    _, data, err := connection.Read(ctx)
    if err != nil { t.Fatal(err) }
    var frame terminalServerMessage
    if err := json.Unmarshal(data, &frame); err != nil { t.Fatal(err) }
    if frame.Type == "size" { seenSize = frame.Columns == 120 && frame.Rows == 40 }
    if frame.Type == "output" { if !seenSize { t.Fatal("output preceded authoritative size") }; break }
}
```
- [ ] **Step 2: Run `cd cloud && go test ./internal/httpapi -run 'TerminalViewer|TerminalRelay'`; expect the new tests to fail.**
- [ ] **Step 3: Gate protocol 3's replay on its first viewer frame.** After the WebSocket upgrade and `reset`, read the first frame under a short context timeout, decode with a strict `viewer` parser, call `UpsertTerminalViewer`, then start the existing input/output goroutines. Zero/unmeasured fit is valid; an unknown role, omitted visibility, non-integer/out-of-range dimension, or wrong first frame closes with `websocket.StatusProtocolError`. A protocol-2 socket follows the old path. Parse into pointer fields so omitted `visible` is distinguishable from `false`:

```go
type viewerFrame struct {
    Type string `json:"type"`
    Role string `json:"role"`
    Visible *bool `json:"visible"`
    Columns *uint16 `json:"columns"`
    Rows *uint16 `json:"rows"`
}
```

- [ ] **Step 4: Make output wait for a committed grid.** Extend the output pump's protocol-3 branch: before replay, read `TerminalGrid` and write `size` if nonzero; if zero, keep the socket in `starting` and check again on its existing ticker until a valid proposal arrives. Keep polling the terminal's open/failed state while waiting, so the existing ready/startup deadline still reports real terminal failure rather than treating missing dimensions as a failed process. Thereafter compare grid with the last sent pair on size notification and on a bounded polling fallback, emit only changed `size` frames, and preserve existing `writeMu` serialization with `output` and `ready`. Add `Columns`/`Rows` to `terminalServerMessage` with `omitempty`.
- [ ] **Step 5: Refresh and release viewer leases.** Extend `readTerminalInput` so protocol-3 `viewer` updates call `UpsertTerminalViewer` even on a read-only attachment; only actual `input` retains the `terminal:operate` gate. A per-socket timer (for example a 20-second tick with a 60-second lease) refreshes the last state. Treat temporary `ErrWorkerUnavailable`/`ErrConflict` as retryable on the next tick; do not disconnect an otherwise healthy output stream. On socket close, call `RemoveTerminalViewer` with a bounded uncancelled context. Register `ao_terminal_size` with the existing Postgres listener; signal the local output pump and retain polling for missed NOTIFY/cross-replica recovery.
- [ ] **Step 6: Run `cd cloud && go test ./internal/httpapi ./internal/postgres ./internal/terminalview` and `go vet ./internal/httpapi ./internal/postgres ./internal/terminalview`; expect pass.**
- [ ] **Step 7: Commit only task files** with `feat(cloud): add viewer-aware terminal protocol`.

### Task 4: Desktop publishes active/parked ownership

**Files:** Modify `frontend/src/renderer/lib/terminal-mux.ts`, `frontend/src/renderer/lib/cloud-terminal-mux.ts`, `frontend/src/renderer/hooks/useTerminalSession.ts`; test `frontend/src/renderer/lib/cloud-terminal-mux.test.ts`, `frontend/src/renderer/hooks/useTerminalSession.test.tsx`.

**Interfaces:** Add optional `setViewerState?: (id:string, visible:boolean, cols:number, rows:number) => void` and `onAuthoritativeSize?: (id:string, listener:(cols:number,rows:number)=>void) => () => void` to `TerminalMux`; only Cloud implements them. Its `open`/`resize` send protocol-3 viewer frames, never protocol-2 resize frames. The hook calls `setViewerState` on park/reactivation even when xterm's grid is unchanged, and tracks the server grid separately from its own fitted grid.

- [ ] **Step 1: Write failing mux tests.** Assert URL has `protocol=3`; first socket-open frame is primary viewer state; `open(0,0)` does not claim size; `setViewerState(false,120,40)` sends a parked frame; `setViewerState(true,120,40)` restores ownership despite an unchanged grid. A server `size` frame must reach `onAuthoritativeSize` without emitting a new client resize. Retain existing cursor/output tests with updated expected frames:

```ts
expect(paramOf(ws, "protocol")).toBe("3");
mux.setViewerState?.("agent", false, 120, 40);
mux.setViewerState?.("agent", true, 120, 40);
expect(sentJSON(ws).slice(-2)).toEqual([
  { type: "viewer", role: "primary", visible: false, columns: 120, rows: 40 },
  { type: "viewer", role: "primary", visible: true, columns: 120, rows: 40 },
]);
```

- [ ] **Step 2: Write a failing hook test.** Start visible at 120×40, park, return visible with the same 120×40 and no xterm `onResize` event; assert the mock Cloud mux sees `false` then `true`. A Local mock without `setViewerState` must retain the prior resize behavior. Extend the existing hook test mux fake with `viewerStates: Array<{visible:boolean;cols:number;rows:number}>`, then assert:

```ts
view.rerender({ daemonReady: true, isVisible: false });
view.rerender({ daemonReady: true, isVisible: true });
act(() => view.result.current.syncVisibleSize(120, 40));
expect(muxes[0].viewerStates.slice(-2)).toEqual([
  { visible: false, cols: 120, rows: 40 },
  { visible: true, cols: 120, rows: 40 },
]);
```
- [ ] **Step 3: Run `cd frontend && npm test -- --run src/renderer/lib/cloud-terminal-mux.test.ts src/renderer/hooks/useTerminalSession.test.tsx`; expect red.**
- [ ] **Step 4: Implement the Cloud mux state cache and hook calls.** Cache `{visible,cols,rows}` in the Cloud mux; send it on WebSocket `open`, on `open`/`resize`, and on explicit `setViewerState`. Validate positive integer server `size` frames and notify size listeners, but keep the hook's own `lastPublishedGrid` separate from the latest server grid. The wire frame is:

```ts
const sendViewer = () => sendJSON({ type: "viewer", role: "primary", visible: viewer.visible, columns: viewer.cols, rows: viewer.rows });
```

  In the hook's existing hidden `useLayoutEffect`, call `r.mux?.setViewerState?.(r.handle, false, r.lastPublishedGrid?.cols ?? 0, r.lastPublishedGrid?.rows ?? 0)`; on `syncVisibleSize(cols,rows)`, call the same method with `true` BEFORE the duplicate-grid early return. Subscribe to `onAuthoritativeSize` to record `r.authoritativeGrid` (a separate nullable runtime field), and clear it on reconnect; this must never be mistaken for the desktop's natural fit or sent back as a proposal. On initial `mux.open` set primary viewer state from `visible/openCols/openRows`. Do not force hidden xterm fits or change the Local mux.
- [ ] **Step 5: Run the two focused Vitest files plus `cd frontend && npm run typecheck && npm test`; expect pass.**
- [ ] **Step 6: Commit only task files** with `feat(desktop): hand off cloud terminal sizing when parked`.

### Task 5: Mobile follows the authoritative grid and zooms locally

**Files:** Modify `packages/mobile/lib/cloud/terminal.ts`, `packages/mobile/lib/cloud/terminal.test.ts`, `packages/mobile/lib/session/CloudTerminalSessionScreen.tsx`, `packages/mobile/lib/session/TerminalSessionScreen.tsx`; create `packages/mobile/lib/session/terminalEnhanceScript.ts` and its test.

**Interfaces:** `createCloudTerminal` accepts `onSize(columns:number,rows:number):void`, sends secondary viewer frames on protocol 3, exposes `setVisible(boolean)`, and treats its `resize` argument as a *proposal*. The screen stores the proposal separately from authoritative size; only `onSize` calls `xtermRef.current?.resize` and updates displayed dimensions. Both screens import the same unchanged zoom/pan script and call existing `adjustTerminalViewport` for +/- controls.

- [ ] **Step 1: Add failing terminal-client tests.** On socket open, expect `{type:"viewer",role:"secondary",visible:true,columns:55,rows:39}`; on a server `size` frame expect `onSize(120,40)`; a natural fit change to 54×38 must send a viewer proposal but must not call `onSize`; reconnect must resend the latest proposal. Update current protocol-2 URL and resize expectations only for the new protocol-3 behavior:

```ts
terminal.resize(55, 39);
await terminal.connect();
const socket = FakeSocket.instances[0]; socket.open();
expect(socket.sent[0]).toBe('{"type":"viewer","role":"secondary","visible":true,"columns":55,"rows":39}');
socket.message({ type: "size", columns: 120, rows: 40 });
expect(sizes).toEqual([[120, 40]]);
terminal.resize(54, 38);
expect(sizes).toEqual([[120, 40]]);
terminal.setVisible(false);
expect(JSON.parse(socket.sent.at(-1)!)).toEqual({ type: "viewer", role: "secondary", visible: false, columns: 54, rows: 38 });
```

- [ ] **Step 2: Add a failing pure script test.** Assert the new `terminalEnhanceScript(platform)` output contains the existing `__aoAdjustTerminalZoom`, `FRESSH_DIMS`, and pinch handling; the two screen imports are verified by typecheck. This guards the Local extraction against accidental feature loss:

```ts
expect(terminalEnhanceScript("ios")).toContain("__aoAdjustTerminalZoom");
expect(terminalEnhanceScript("ios")).toContain("FRESSH_DIMS");
expect(terminalEnhanceScript("android")).toContain("mode = 'pinch'");
```
- [ ] **Step 3: Run `cd packages/mobile && npm test -- lib/cloud/terminal.test.ts lib/session/terminalEnhanceScript.test.ts`; expect red.**
- [ ] **Step 4: Extract the Local WebView script without changing its body or Local fit/resize logic.** Move the existing `TERMINAL_ENHANCE_JS` literal to `terminalEnhanceScript.ts`; export `terminalEnhanceScript(platform:"ios"|"android")` returning `var IS_ANDROID=${platform === "android"};\n${TERMINAL_ENHANCE_JS}`. The Local screen replaces its inline string with that call. Move no unrelated UI code.
- [ ] **Step 5: Implement Cloud secondary rendering.** `createCloudTerminal` uses `protocol=3`, sends its current proposal on open, sends new proposals on natural fit changes, and invokes `onSize` only for validated positive integer `size` frames. Its `setVisible` sends a viewer update with the last natural fit. In `CloudTerminalSessionScreen`, use Expo Router's `useFocusEffect` to call `setVisible(true)` on focus and `setVisible(false)` on blur, keep `lastSize` as natural fit, add `authoritativeSize`, remove `xtermRef.current?.resize` from `applySize`, and resize xterm from `onSize`/`onInitialized`. Queue output until both WebView initialization and first authoritative size, including after a reconnect/reset; clear the prior authoritative-size gate on reset so a stale width cannot render the new replay. Use `terminalEnhanceScript` as `injectedJavaScript`, and add the Local screen's compact +/- zoom controls next to authoritative dimensions. The zoom controls call `adjustTerminalViewport(xtermRef.current, direction)`; they do not call `terminal.resize`.
- [ ] **Step 6: Run focused mobile tests, `cd packages/mobile && npm run typecheck && npm test`; expect pass.** Test a device/emulator if available: desktop-size `size` frame must show a 120-column grid on phone with usable pan/zoom; a later 55-column frame must reflow to phone size.
- [ ] **Step 7: Commit only task files** with `feat(mobile): mirror cloud terminal grid with zoom`. Because the Cloud terminal files already exist as untracked work in this checkout, inspect their diff and stage them explicitly; do not stage unrelated mobile changes.

### Task 6: End-to-end handoff gate

**Files:** No feature files; fix only a task-owned file if a verification failure identifies a regression.

**Interfaces:** End-to-end behavior from the spec across one desktop and one phone, then across desktop session A/B switching.

- [ ] **Step 1: Run `cd cloud && go test ./... && go vet ./...`, `cd frontend && npm run typecheck && npm test`, and `cd packages/mobile && npm run typecheck && npm test`.** Record exact failures; do not call an unavailable Docker/device gate passed.
- [ ] **Step 2: Verify the exact sequence manually against a scratch Cloud environment, without deploying production:** desktop A at 120×40, mobile A at 55×39, desktop switches to B, then returns to A. Observe A's server `size` frames as 120×40 → 55×39 → 120×40, desktop A never shrinks while visible, phone controls zoom locally, and B's grid does not alter A. Disconnect/reconnect either viewer and verify stale lease expiry.
- [ ] **Step 3: Inspect `git status --short` and `git diff --name-only b3e85d403..HEAD`; confirm no `docs/` changes and no unrelated user files were staged.** Report unrun device, Docker, or Cloud checks honestly. Do not publish or deploy.

## Self-review coverage

Tasks 1–3 cover server election, leases, malformed frames, authoritative push, retry, worker-epoch fencing, and protocol-2 compatibility. Task 4 covers desktop parked/visible handoff, including unchanged local grid. Task 5 covers secondary proposals, server-sized rendering, zoom/pan, and Local behavior preservation. Task 6 covers the actual two-session handoff and verification gaps. No files under `docs/` are part of this plan.
