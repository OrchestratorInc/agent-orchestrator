# AO and T3 Code chat performance

Comparison on 2026-10-03: AO `5976aaa98bc18b2d6f5e8319279abde374c2d85a`
and public T3 main `e8545b293b79cdd73d30ef921db149ef1b5abee0`, checked out
at `~/T3Projects/t3code`. This compares source architectures and measures AO
before/after; it is not a benchmark against the installed T3 nightly build.

## Why T3 has an advantage

| Path | AO baseline | T3 main |
| --- | --- | --- |
| Live conversation updates | Provider event → durable projection → trigger change log → 100 ms CDC poll → SSE invalidation → up to 150 ms refresh coalescing → HTTP snapshot → query selection → React. | Initial snapshot plus sequenced thread subscription; received event batches are reduced into thread state with one state write per batch. Ordinary updates do not need a separate snapshot request. |
| Received text | Character playback adds a buffer with a 200 ms scheduling deadline, on top of transport latency. | Assistant rows pass the received message text to Markdown. No analogous character playback queue appears in that row path. |
| Long histories | All loaded turns remain mounted. Local infinite-query invalidation refetches loaded pages. | LegendList virtualizes transcript rows and expanded tool groups; earlier history is fetched separately from the live event stream. |
| Streaming Markdown | React Markdown parses the complete growing text again for each playback update. | A per-renderer parser cache reuses completed top-level fenced prefixes while preserving the complete document transform pipeline. |
| Update identity | Inline query selection recreates merged lists during observer renders. A fresh link-context object notifies context consumers even when link settings have not changed. | Thread reducers preserve unaffected projection state, and memoized timeline rows receive stable callbacks through shared context. |

AO sources: [CDC poller](../../../backend/internal/cdc/poller.go),
[event transport](../../../frontend/src/renderer/lib/event-transport.ts),
[conversation query](../../../frontend/src/renderer/hooks/useConversation.ts),
[timeline](../../../frontend/src/renderer/components/chat/ChatWorkspace.tsx), and
[text playback](../../../frontend/src/renderer/components/chat/ChatTimelineItems.tsx).

T3 sources, pinned to the inspected revision:
[thread synchronization](https://github.com/pingdotgg/t3code/blob/e8545b293b79cdd73d30ef921db149ef1b5abee0/packages/client-runtime/src/state/threads.ts),
[timeline](https://github.com/pingdotgg/t3code/blob/e8545b293b79cdd73d30ef921db149ef1b5abee0/apps/web/src/components/chat/MessagesTimeline.tsx),
[Markdown renderer](https://github.com/pingdotgg/t3code/blob/e8545b293b79cdd73d30ef921db149ef1b5abee0/apps/web/src/components/ChatMarkdown.tsx), and
[incremental parser](https://github.com/pingdotgg/t3code/blob/e8545b293b79cdd73d30ef921db149ef1b5abee0/apps/web/src/markdown-incremental.ts).

These paths explain extra application work and latency; they do not prove which
component dominates a particular user's session. Provider inference, harness
startup, and actual desktop frame/input latency still need matching traces.
Both apps use React 19 and React Markdown 10. Replacing React or rewriting the
Go daemon is not supported by this evidence. T3 main uses Electron 44 while AO
uses Electron 33; that difference alone does not establish a cause.

## Changes made in this checkout

- Adopt T3's conservative incremental parser and parity tests, retaining the MIT
  attribution/license. Cache only a closed top-level fence followed by a blank
  line. References and footnotes require a full parse; CR/BOM input also falls
  back. Clone cached nodes before transforms so GFM and AO session links cannot
  mutate the cache. Settled messages use the ordinary parser. This preserves
  document-wide Markdown behavior instead of independently rendering fragments.
- Reduce the text playback scheduling deadline from 200 ms to 50 ms. Keep
  grapheme boundaries, provider corrections, completion flushing, reduced
  motion, and StrictMode cleanup. A hidden or blocked renderer can still miss
  this deadline; it is not a guaranteed display latency.
- Memoize link context and the conversation selector. Return an already ordered
  single page directly, avoiding redundant merging/sorting. Avoid segmenting
  plain strings that contain no emoji when rendering prose.
- Extend the existing opt-in browser harness with a code-heavy Markdown workload.

## Measurements

Real AO components, synthetic input, Vite development renderer, one Playwright
worker, three serial repetitions per before/after streaming and Markdown
workload. Apple M4, 10 logical CPUs, 16 GiB RAM, macOS arm64, Node 22.23.2,
Chromium 148.0.7778.96, 1440 × 1000 viewport. No test/build suites overlapped
these final benchmark runs. Values below are descriptive medians and ranges.

| Workload | Before | After |
| --- | ---: | ---: |
| 997-character received Unicode burst, time until exact DOM text | 219.6 ms (217.5–220.7) | 70.0 ms (67.4–70.1) |
| 40 streaming renders following a completed 40,013-character code prefix, total synchronous render work | 295.2 ms (288.2–302.2) | 42.3 ms (41.4–48.3) |

The burst is visible about 68% sooner in this workload. Code-heavy render work
falls about 86%. These percentages are not whole-app or T3-relative speedups.
The existing 250-turn scroll workload showed no improvement: its maximum frame
gap was 20.1 ms in the single baseline sample versus 20.7–20.9 ms after. All 250
turn anchors remained mounted. Transport timing and highlighting are unchanged.

Raw samples are in `/tmp/ao-chat-perf` on the measurement host. Reproduce from
`frontend/` with lockfile dependencies and Playwright Chromium:

```sh
AO_PERF_BENCH=1 AO_PERF_LABEL=after AO_PERF_DIR=/tmp/ao-chat-perf \
  AO_E2E_PORT=5187 npx playwright test e2e/chat-performance.spec.ts \
  --workers=1 --repeat-each=3
```

For a baseline, use an isolated checkout at the AO revision above and copy the
same benchmark spec/harness into it. Timings are hardware-dependent evidence,
not CI thresholds. The previous [responsiveness report](README.md) describes
earlier optimizations; its older measurements are a separate experiment.

## Work required to establish parity

1. **Deliver committed conversation updates directly.** Add an explicit durable
   change cursor and typed updates through the daemon API. Batch item/turn
   revisions, removals and metadata; atomically apply them to the renderer's
   query state. Bootstrap/recover with snapshots, detect cursor gaps, and reset
   on branch/provider changes. Keep DB-trigger CDC as the durable authority.
   SSE can carry this; switching to WebSockets alone does not remove refetches.
   Item sequence is an ordering key, not a revision cursor: an existing message
   or old running tool can change without becoming the newest item.
2. **Bound long-transcript work.** Introduce timeline virtualization or measured
   viewport containment, including prompt spacers, minimap positions, scroll
   anchoring, expanded tools, editing and images. Preserve transcript search and
   selection deliberately; simply unmounting offscreen text changes those
   behaviors. The current benchmark explicitly asserts all turns stay mounted
   and must evolve alongside the search/selection contract.
3. **Benchmark both packaged apps with the same replay.** Use identical provider
   events, long prose/code/tool output, 250/1,000/5,000 turns, background sessions
   and typing during streaming. Record provider-received → DOM latency, input
   latency, long tasks, frame gaps, bytes transferred, request count and memory.
   Define parity against T3's measured results on the same machine and workload.

Refreshing only the latest page is intentionally excluded: prior AO investigation
identified older row updates and shifted pagination boundaries that could be
lost. Global poll-frequency reductions and an Electron upgrade also need their
own measurements before being treated as fixes. The implemented changes are a
measured first step; transport and transcript scaling remain open.

## Validation of this change

- Full frontend Vitest suite: 377 files passed, 5,992 tests passed, 7 skipped.
- Frontend and E2E TypeScript checks passed.
- Production Vite renderer build passed; normal existing chunk-size warnings.
- All 15 after-change browser benchmark runs passed exact-text and mounted-turn
  checks; Markdown parity tests compare AST positions and rendered HTML while
  streaming, editing, and adding definitions.
- Renderer smoke: 64/66 passed initially. The clone destination-picker timeout
  passed on retry. Composer pointer-drag selection still fails on retry and with
  the original three production source files restored from the baseline commit;
  the failure is reproducible without these changes. The smoke suite is therefore
  not reported as fully passing.
- The collaborative T3 browser also rendered the Unicode burst and restored the
  copy action with exact text. Its timing is excluded from the controlled table
  because full validation jobs were running at the time.
- `ao preview` was attempted and refused because this T3 thread has no
  `AO_SESSION_ID`; the collaborative browser supplied the renderer inspection.

Validation used the local Node 22 runtime, not the CI-pinned Node 24 environment.
No PR, remote CI, packaged AO/T3 comparison, or release validation was performed.
