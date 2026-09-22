# Cloud terminal viewer sizing and mobile zoom

Status: approved for implementation on 2026-09-22. This spec does not authorize deployment or a production rollout.

## Goal

Keep a Cloud agent's single PTY at the visible desktop terminal's full grid while that desktop pane is active. A phone viewing the same terminal renders that grid with local crop, pan, and zoom, without shrinking the PTY. When the desktop parks the pane by switching sessions, the phone's natural grid becomes authoritative. Returning to the desktop restores its grid. This applies per terminal, so switching desktop session A to session B changes ownership of A but does not couple their grids.

## Existing behavior and boundary

Local terminals already elect a primary grid and push it to secondary viewers (`backend/internal/terminal/manager.go`). Local mobile renders that pushed grid and offers fit, zoom, and pan (`packages/mobile/lib/session/TerminalSessionScreen.tsx`). Cloud currently queues every nonzero `resize` received on its WebSocket (`cloud/internal/httpapi/terminal_handlers.go`), and the worker applies it directly with `pty.Setsize`. Cloud mobile sends its 55-column fit and resizes xterm to that fit; the desktop retains hidden Cloud sockets, so merely counting connections cannot express active ownership. Cloud control-plane replicas may accept different viewers of the same terminal.

## Decision and alternatives

Use a Postgres-backed, per-terminal viewer lease and authoritative grid. This extends the existing Cloud terminal protocol without duplicating the harness or introducing a second PTY. A control-plane-local viewer map is smaller but incorrect across replicas. A mobile-only zoom/crop change is smaller but still allows its `resize` frame to shrink the desktop's PTY and cannot handle desktop parking. Separate terminal processes would provide independent native layouts but would no longer be one interactive harness session; that is outside this work.

## Protocol and arbitration

Add an opt-in `protocol=3` Cloud terminal socket. It retains protocol 2's structured output/input frames and adds client `{type:"viewer",role,visible,columns,rows}` and server `{type:"size",columns,rows}` frames. The viewer ID is generated server-side for each socket, not accepted from a client. `role` is `primary` for desktop and `secondary` for mobile. The first viewer frame is sent on socket open and resent after reconnect; later frames update the same viewer. A parked desktop sends `visible:false` without zeroing its last measured grid; a newly visible desktop sends `visible:true` and its current grid even if the dimensions have not changed. Closing a socket removes its viewer. User input and output subscriptions remain available to parked sockets as today. Protocol 2 remains unchanged during rollout.

Persist each socket's viewer state with a short expiry under its Cloud terminal ID. Each state change, detach, or periodic lease refresh atomically prunes expired viewers and elects one grid: largest-area visible primary, else largest-area visible secondary. Always take both dimensions from the same viewer. A zero/unmeasured grid is ineligible. If none is eligible, retain the last authoritative grid and do not send a 0x0 PTY resize. The transaction serializes elections for one terminal and queues a worker `terminal.resize` only when the elected grid changes. A crashed viewer expires; an attached viewer refreshes its lease and triggers re-election. An old worker epoch's viewers cannot affect the replacement terminal.

For protocol 3, the control plane processes the first viewer frame before starting replay; it sends the current authoritative `columns`/`rows` to that viewer before replaying output, then sends size changes to every viewer, including those connected to a different replica. If the first frame is missing or malformed, close that protocol 3 attachment with a protocol error rather than guessing its role. If no authoritative size exists yet, wait for the first valid viewer fit while keeping the socket live and showing the existing starting state. Use the existing output wake/poll pattern or an equivalent cross-replica notification; do not rely on a process-local broadcast alone. Size messages describe the grid selected for the PTY, not a viewer's proposed fit. Clients apply the authoritative grid before rendering replay that contains full-screen TUI output. A failed/temporarily unavailable worker resize must remain retryable; the server must not silently report a grid that can never be applied.

Legacy protocol-2 Cloud clients keep their current direct-resize behavior during rollout. This compatibility cannot protect a new desktop from an old mobile client; the new control plane should ship before updated desktop/mobile clients, and the new mobile client must use protocol 3 with the secondary role. After the clients have upgraded, protocol 2 can be retired separately. No change is required to Local terminal arbitration.

## Client behavior

Desktop Cloud terminal mux advertises primary role and current visibility. The existing `isVisible` transition in `useTerminalSession` sends parked/visible state independently of xterm's `onResize`; on activation it reasserts the current fitted grid even if unchanged. It consumes authoritative size messages for the shared PTY state without fitting a hidden pane or allowing a hidden pane to re-claim ownership. Local mux behavior remains unchanged.

Mobile Cloud terminal advertises secondary role. Its FitAddon reports the phone's natural grid as a proposal only; the xterm render grid follows the server's authoritative size. Reuse the Local terminal's zoom/pan interaction and fit-to-width/1:1 controls, with extraction only where needed to avoid copying a large injected script. On a server size transition, preserve output order and resize the WebView to the new authoritative grid; the viewport recomputes scale and pan for the current device size. The status-bar dimensions show the authoritative grid, not merely the phone proposal.

## Failure behavior

- A viewer with no measured dimensions receives output but does not influence PTY size.
- Disconnect/reconnect creates a new viewer identity, reapplies role/visibility/fit, and receives current authoritative size before replay; stale viewer leases expire.
- A hidden desktop that keeps its WebSocket open does not block a phone from taking over.
- If the phone disconnects while desktop is parked, keep the last grid until another eligible viewer appears.
- Visibility must reflect actual pane activation, not only application focus. A desktop session switch parks the old pane and makes the new pane visible; no cross-session resize is sent.
- The worker and all clients see a single grid per terminal. Independent simultaneous native TUI layouts are explicitly not a goal.

## Verification and rollout

Add store/arbitration tests for primary/secondary election, same-grid dedupe, parked handoff, detach, lease expiry, and worker-epoch isolation. Add Cloud WebSocket tests for initial and cross-replica size delivery, malformed/zero dimensions, and reconnect. Add desktop tests for visible-to-parked-to-visible transitions with unchanged xterm dimensions. Add mobile tests for secondary proposals, authoritative rendering, and fit/zoom behavior; run mobile typecheck and relevant full suites. Manually verify two Cloud sessions: desktop A plus mobile A (desktop retains full grid), switch desktop to B (mobile A changes to phone grid), return to A (desktop A regains full grid and mobile A zooms it), and disconnect/reconnect either viewer. Deploy the control-plane protocol before client releases. Do not edit or commit anything under `docs/`.
