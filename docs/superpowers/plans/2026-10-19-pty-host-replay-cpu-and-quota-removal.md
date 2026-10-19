# Stop the terminal-replay CPU burn, then remove the 90% CPU quota

**Goal:** make the desktop app stop burning CPU (and GPU) with agent sessions running, so the self-imposed 90% `CPUQuota` on this machine can be removed without the app eating the machine.

**Status of this document:** Tasks 1–3 (PR-1, backend) are implemented on branch `perf/pty-host-replay-bounds`; Tasks 4–5 remain. Implementation deltas from the plan below: the tail cap is applied by the host handlers via a shared `capTailBytes` helper (not inside `Ring.Tail`), and the classification timers are per-`host` fields (`firstFrameTimeout`, `legacyViewerGrace`, defaults `defaultFirstFrameTimeout`/`defaultLegacyViewerGrace`) instead of package vars, so tests configure them before `Serve` starts.

**Update 2026-10-19 (PR review fixes, commits `79c45912b` + `0d3ae82be`):** two contract corrections. Input-first connections are input-only: their `MsgTerminalInput` bytes are dispatched to the PTY, they are never replayed and never broadcast to, and a later input never upgrades a connection — only `MsgResize` (plus the silent and `MsgStatusReq`-grace fallbacks) marks a viewer. Ring storage entries carry a logical-line-end flag, so `Tail(n)` counts completed logical lines and `Snapshot()` excludes the in-progress line including its chunks; `Replay()` stays byte-identical to the retained stream. The classification table and task steps below are updated to match.

## Diagnosis (already done, evidence from the live machine)

The "app eating CPU" is dominated by a **terminal-history replay amplifier** between the daemon and each session's `ao pty-host`, not by the Electron UI.

Measured on 2026-10-19 (deb 0.14.2, two live opencode sessions):

| Metric | Value |
| --- | --- |
| pty-host reads from agent PTY | 233 KB / 20 s (36 MB lifetime) |
| pty-host writes to daemon | **31.7 MB / 20 s** (7.8 GB lifetime, ~215x amplification) |
| daemon socket reads | **89 MB / 20 s (~4.5 MB/s)** and it discards almost all of it |
| CPU | each pty-host ~19% of one core; daemon 6–8%; kernel loopback systime on top |
| Single frame observed | `writev(fd=16, iov_len=36,058,748)` → 1 MB → `EAGAIN` → repeat |
| Contrast: idle AppImage chat-hosts | 7 MB written total — normal profile |

Mechanism (all verified in source):

1. `conpty/host.go:449` `handleConn` queues **the entire ring replay** (`Ring.Replay()`, mode prefix re-applied by `h.modes.Restore`) to **every** new connection, as one frame.
2. `ring.go` bounds retention by **lines** (`MaxOutputLines = 1000`), not bytes. Full-screen TUIs (opencode, Claude Code) repaint with cursor control and few newlines, so a single "line" grows to megabytes; the observed ring was ~36 MB.
3. `conpty/client.go:123` `clientReadOutput` opens a **fresh connection per probe** (GetOutput / GetStyledOutput / Status), reads until its response frame, and discards everything else. It can only reach the response **after** draining the whole replay.
4. Probes come from hot paths: activity observer (`observe/activity/observer.go`, 30 s tick, every live session — board status), prompt-readiness waits during message delivery (`session_manager/manager.go:6389`, 200 ms polls), interface transitions, agent switching, review launcher, and the reaper's liveness status probes (`clientIsAlive`).
5. Viewer attaches (`attach.go`) pay the same full replay once per pane open — by design, but 36 MB is far above a sane scrollback.

Consequences: CPU cost scales with **session history size × probe frequency**, grows all day, and (with the quota in place) competes with the UI and the agents for 0.9 cores — which is why the app felt both "hungry" (unlimited) and "slow" (limited).

Secondary observations, to keep in scope:

- Renderer main thread ~6% and GPU process ~3–5% while the UI sits there with agents streaming: candidates verified in code are the terminal path (`XtermTerminal.tsx` `cursorBlink: true`, `lib/terminal-webgl-pool.ts` WebGL renderers, replay bursts re-parsed on pane open) plus always-on infinite CSS animations (`status-pulse`, shimmer/pulse sets in `styles.css`). Needs a trace before changing anything.
- Two app instances were found running (deb + AppImage); the AppImage's daemon is gone but its `chat-host` trees are still alive 13+ h later, holding memory and CPU in their old 90% cgroup.

## Fix design

Three layers, in order of value. Layer 1+2 are the real fix; layer 3 keeps the leftover burn low after the replay is bounded.

### Layer 1 — bounded ring, bounded tails (safety net)

- Bound the ring by **bytes**, not just lines: `MaxRingBytes = 4 MiB` (env override later if anyone needs more scrollback).
- Split oversized partial lines at `MaxRingLineBytes = 64 KiB` so line-based eviction actually bounds TUI repaint streams. Splitting only moves the partial line into storage chunks (each entry carries whether it ends a logical line); `Replay()` joins every chunk plus the partial line, so the retained byte stream is unchanged, while `Tail(n)` counts completed logical lines and `Snapshot()` still excludes the in-progress line, including its chunks.
- Cap probe/tail responses at the host: `MaxTailBytes = 128 KiB` suffix (keeps the most recent content, which is what prompt/activity patterns need). Applies to `MsgGetOutputRes` and `MsgGetStyledOutputRes`.
- Send replays in `MaxReplayChunk = 256 KiB` frames instead of one potentially megabyte-scale iovec.

After this layer, even a worst-case misclassification costs ≤4 MB per connection instead of unbounded.

### Layer 2 — connection classification: probes never pay the replay

New protocol message `MsgAttach = 0x0b` (client → host), JSON `{cols, rows, replay}`:

- `conpty.Runtime.Attach` (the only real viewer) sends it as the **first frame**, with `replay: true`, keeping today's Resize + StatusReq frames right after for old-host compatibility.
- Host behavior on the **first frame** of a connection:

  | First frame | Classification | Replay | Registered as viewer |
  | --- | --- | --- | --- |
  | `MsgAttach` | viewer | only if `replay:true` | yes |
  | `MsgResize` (legacy viewer) | viewer | yes (today's behavior) | yes |
  | `MsgTerminalInput`-first | input-only | **no** | no (dispatched to the PTY; never broadcast to) |
  | `MsgGetOutputReq` / `MsgGetStyledOutputReq` / `MsgStatusReq` / `MsgKillReq` | probe | **no** | no (responses via `sendTo` only) |
  | nothing within `firstMsgTimeout` (2 s) | legacy silent viewer | yes | yes |

- A non-viewer connection that later sends `MsgResize` is upgraded to viewer (register + replay): compatibility net for a legacy viewer that attached before any grid was known. A later `MsgTerminalInput` never upgrades a connection — it is dispatched to the PTY and the connection stays input-only.
- Accepted compatibility boundary: a hypothetical third-party client that sends `MsgTerminalInput` first without any hello or `MsgResize` no longer receives a replay; every in-repo client sends the hello (new attach) or `MsgResize`/`MsgStatusReq` first (pre-hello attach) before any input.
- `MsgStatusReq`-first connections get a `legacyViewerGrace` (~1 s): if the connection is **still open** after the status response, upgrade to viewer. Real probes (reaper, CLI) close within milliseconds; a legacy `rows==0` viewer stays open and self-heals.
- `record()`/`broadcast()` (`enqueueAllLocked`) skip non-viewer clients — probes and input-only connections stop receiving live output entirely.
- Probe connections are still added to `h.clients` (so `sendTo` works) but never count for `applyLargestLocked` sizing.
- `MsgAttach` with `cols/rows > 0` must feed the same path as `MsgResize` (`applyLargestLocked`), because `--start=attach` deferred PTYs start on the first grid report.
- Bump `conPTYHostProtocolVersion` 2 → 3 (reported in `StatusRes`).

**Cross-version matrix** (matters across in-place upgrades, where an old pty-host process keeps serving a new daemon):

| | old host | new host |
| --- | --- | --- |
| old client (probe) | replay on connect (today) | **no replay** — fixed automatically |
| old client (viewer) | replay on connect | Resize-first → viewer; Status-first → grace upgrade |
| new client (probe) | replay on connect (harmless, discarded) | **no replay** |
| new client (viewer) | hello ignored (host has no default case), replay on connect + Resize — works | hello → sized + replay once |

Rollback is safe: reverting the host restores v2 behavior; the client's hello is a no-op on old hosts.

### Layer 3 — renderer/GPU burn (diagnose first, then targeted fixes)

No speculative UI edits. First measure with a CDP performance trace (chrome-devtools MCP) on an idle board and with a pane open + agent streaming; then apply only what the trace justifies. Expected candidates, in order:

1. Pause terminal processing for non-visible panes (hidden workspace / collapsed inspector): stop feeding xterm + WebGL while not visible.
2. Terminal render batching: coalesce incoming PTY bytes into ≤60 Hz xterm writes; ensure `terminal-webgl-pool` only holds contexts for visible panes and releases them on hide/close.
3. Always-on infinite animations: pause offscreen ones (IntersectionObserver / workspace visibility) and confirm they are compositor-only.
4. Verify hardware acceleration is actually in use (`chrome://gpu` equivalent via CDP `SystemInfo`).

## Tasks

### Task 1 — Ring byte bounds, chunked replays, tail caps

**Files:** `backend/internal/adapters/runtime/conpty/ring.go`, `ring_test.go`, `host.go` (replay/tail send paths), `host_test.go`.

**Steps:**

1. Add `MaxRingBytes`, `MaxRingLineBytes`, `MaxTailBytes`, `MaxReplayChunk` constants; track total bytes in `Ring` (including newlines).
2. `Append`: flush `partialLine` into `lines` when it exceeds `MaxRingLineBytes`; evict whole lines from the front while total > `MaxRingBytes`.
3. `Tail(n)`: return the last n completed logical lines, reassembling entries split at `MaxRingLineBytes` and excluding the in-progress line's chunks. The host handlers cap the joined result at `MaxTailBytes` (see the status note above).
4. `handleConn`: encode the (mode-prefixed) replay into ≤`MaxReplayChunk` frames under the **same single `h.mu` hold** used today (bounded work, ~4 MB worst case; keeps the no-drop snapshot/register invariant from #5445). Verify `clientState.out` capacity (256 entries) comfortably fits the chunk count (~16).
5. Apply `MaxTailBytes` to `MsgGetOutputRes` and `MsgGetStyledOutputRes` payloads.
6. Tests: eviction by bytes; oversized partial split; `Replay()` byte-identity across the split; `Tail`/`Snapshot` completed-logical-line semantics (chunked lines reassembled, in-progress chunks excluded); tail suffix cap; existing replay/mode-prefix tests stay green.

### Task 2 — Attach hello + host classification

**Files:** `backend/internal/adapters/runtime/conpty/proto.go`, `host.go`, `host_test.go`, `host_main.go` (config if needed), `client.go`.

**Steps:**

1. `proto.go`: `MsgAttach = 0x0b`, `AttachPayload{Cols, Rows int, Replay bool}`, protocol version 3.
2. `handleConn`: read the first frame with a `firstMsgTimeout` deadline (configurable for tests); classify per the table above; clear the deadline afterwards.
3. Viewer path: register as viewer, size the client if given (same path as `MsgResize`, including deferred-PTY start), queue the bounded replay only when wanted.
4. Probe path: register as non-viewer, dispatch the message; skip in `enqueueAllLocked`; never size.
5. Grace upgrade rules: a later `MsgResize` on a non-viewer conn → viewer; a later `MsgTerminalInput` is dispatched to the PTY but never upgrades the connection; `MsgStatusReq`-first conn still open after `legacyViewerGrace` → viewer.
6. Tests (with a shortened `firstMsgTimeout` in the fixture): probe against a multi-MB ring receives exactly the response frame (no `MsgTerminalData`) and bytes-written ≈ response size; hello `replay:false` skips; hello `replay:true` replays; legacy Resize-first replays; silent connection replays after timeout; deferred PTY starts from hello grid; upgrade paths. Update `newTestClient` to send hello for viewer tests; add a probe helper.

### Task 3 — Client side + compatibility

**Files:** `backend/internal/adapters/runtime/conpty/attach.go`, `attach_test.go`, `runtime_test.go`/fakes; comment in `backend/internal/terminal/attachment.go`.

**Steps:**

1. `Runtime.Attach`: send `MsgAttach{cols, rows, replay:true}` before Resize/StatusReq (send even when the grid is unknown, with zeros).
2. Fakes in tests: ignore unknown message types like the real old host does.
3. Update the `terminal/attachment.go` doc comment to reflect the hello.
4. Tests: hello is the first frame; behavior against a fake v2 host (ignores hello) still replays + works; `clientGetOutput`/`clientStatus` unchanged apart from no longer paying replays.

### Task 4 — Renderer/GPU diagnosis and targeted fixes

**Files:** TBD by trace; candidates `frontend/src/renderer/components/XtermTerminal.tsx`, `frontend/src/renderer/lib/terminal-webgl-pool.ts`, `frontend/src/renderer/styles.css`, pane visibility components.

**Steps:**

1. CDP trace: idle board; pane open + streaming agent; note long tasks, frame times, animation counts.
2. Sample renderer/GPU threads (`top -H`) and record before numbers.
3. Apply the justified subset of Layer 3 fixes; re-trace; keep before/after numbers for the PR.
4. Ship as a separate PR if it grows beyond a few files.

### Task 5 — Verify on a dev build, then remove the quota

**Verification build:**

1. Lab worktree per `AGENTS.md` ("Desktop lab"): `npm ci`, `npm run build:daemon -- --dev`, run against `AO_DATA_DIR=/tmp/ao-lab-data`, reproduce the replay scenario (pane open, heavy TUI output, probe churn) and record: pty-host `wchar`, daemon `rchar`, per-process CPU, `cpu.stat` throttling.
2. For real use: `cd frontend && npm run make` for a local deb install, or keep the quota until a release carries the fix.

**Expected outcomes (acceptance):**

- pty-host steady-state write/read ratio ≈ 1 (a viewer attach adds ≤4 MB once; probes add ~0).
- daemon socket reads at idle ≈ live agent output (tens of KB/s), not MB/s.
- pty-host CPU <1–2% at steady state; app-tree idle CPU well under one core with 2 live sessions.
- `cpu.stat` throttled period ratio ~0 at 90% quota with agents idle-ish (proves headroom), then:

**Quota removal runbook (after the fix is verified):**

```bash
# 1. discover everything quota-related
grep -n Exec ~/.local/share/applications/agent-orchestrator.desktop    # systemd-run -p CPUQuota=90%%
systemctl --user list-units --all | grep -i -E 'agent|AppImage' || true
systemctl --user show <scope> -p CPUQuotaPerSecUSec,ActiveState   # scope from the list above

# 2. remove the persistent override (this file exists only to add the quota)
rm ~/.local/share/applications/agent-orchestrator.desktop
update-desktop-database ~/.local/share/applications 2>/dev/null || true

# 3. reset the live scope (empty value resets; CPUQuota=infinity as fallback)
systemctl --user set-property <scope> CPUQuota=
systemctl --user show <scope> -p CPUQuotaPerSecUSec   # expect infinity
cat /sys/fs/cgroup/.../<scope>/cpu.max                # expect "max 100000"

# 4. stale AppImage leftovers (chat-hosts from 2026-10-18) — confirm the sessions
#    are finished, then stop the owning unit or terminate the process trees:
pgrep -a -f "resources/daemon/ao chat-host"   # stale chat-hosts (+children)
cat /proc/<pid>/cgroup                         # find the owning unit before stopping it
```

5. Relaunch from the app menu and confirm the new `app-*.scope` has `cpu.max = max 100000`, then run a normal day of agents and sample `cpu.stat`/top once more.

## Risks, rollback, out of scope

- **Ring byte cap trims TUI scrollback** (by design; a shell still keeps ≥1000 lines at 4 MiB). Follow-up env override if needed.
- **Tail byte cap** keeps the newest content; prompt/readiness patterns live at the tail — run `session_manager` pattern tests to confirm.
- **Protocol compat** is handled in both directions (matrix above); rollback = revert the PR, host returns to v2.
- **Old pty-hosts keep replaying to old daemons** until sessions restart after an upgrade — self-healing, note in the PR body.
- Out of scope: the tmux runtime path, observer/tick cadences (unnecessary once probes are cheap), any quota *increase* (we remove it outright after verification).

## Milestones

1. **PR-1 (backend):** Tasks 1–3, one PR, commits `perf(pty-host): bound the replay ring and tail responses` + `perf(pty-host): skip scrollback replay for probe connections`. Full `npm run lint` + backend tests + race before handoff.
2. **PR-2 (frontend, only if Task 4 finds wins):** hidden-pane gating, WebGL pool hygiene, animation pausing, with before/after traces.
3. **Ops:** install a build with PR-1/PR-2, verify acceptance numbers, remove the quota per the runbook, clean stale instances.
