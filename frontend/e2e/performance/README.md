# Large-chat measurement workloads

These opt-in workloads use real renderer components with deterministic synthetic
data. They do **not** measure navigation through the desktop shell or replace the
five-real-chat before/after measurements required by #7259.

Dependency-free recorder/fixture contract smoke (Node 24; no app or daemon):

```sh
node --test --test-concurrency=1 e2e/performance/chat-open-measurement.node-test.mjs
```

From `frontend/`, with the checkout's Node version and dependencies installed:

```sh
AO_PERF_BENCH=1 AO_E2E_PORT=5199 NODE_OPTIONS=--max-old-space-size=1536 \
  npm run test:e2e -- --grep 'largeTurn' --workers=1
```

Use a free, owned port. Set `AO_PERF_LABEL` to identify the revision and optionally
`AO_PERF_DIR` to an owned output directory outside the repository. Playwright also
attaches measurements to each test result. Repeat on the same host, viewport,
runtime and workload; timings are evidence, not portable CI thresholds.

- `largeTurn`: one completed turn with 1,850 command rows and interleaved Markdown.
  Fixture construction happens before timing. Measures from render start until
  the final durable message intersects the conversation viewport for two frames.
  A missing message or stalled animation frames reject as incomplete after 15s.
- `largeTurnDisclosure`: a smaller 50-command variant checks that collapsed
  bodies are absent, then opens the work, runs and rows and checks every command
  output and interleaved note against the fixture.

The open sample records long-task count/max/total and entries, React Profiler
samples, and `performance.memory.usedJSHeapSize` when available. Unsupported
metrics are `null`, not zero. Long tasks overlapping the measurement window retain
their full reported duration; an initial task may have a negative relative start.
The Profiler wraps the **whole ChatWorkspace**;
`actualDuration` is React render work per commit, `baseDuration` is React's
estimate, and `commitMs` is a timestamp relative to measurement start. None is
commit-phase wall time. Heap is sampled without forced GC and may include prior
allocations. The dev renderer and shared-host load also affect timings.

Each generated sample includes a SHA-256 of the exact UTF-8 serialized view model,
byte/item/turn/tool counts, and observed compact-row flags. `apiCompactOption` is
`null` because the generated workload makes no conversation API request; a real
capture must pass the recorded request option to `fixtureIdentity`. Identity is
computed before timing. No message bodies are retained in the output.

The runner retains exact HEAD, dirty state, current-byte hashes of the explicitly
listed capture/source boundaries, Node/browser versions, viewport, motion and
visibility state, worker count, heap limit, and before/after host load/free memory.
The source hash is **not** a whole-bundle hash. Changing HEAD or a listed source
file during capture fails the run. Record launch flags, cache/GC/warm-up policy,
and any local instrumentation diff alongside each real experiment too.

## Stage capture recipe for an owned lab

`measureChatOpen` is test-only. The application never installs probes. Its
`startStage`, `measureSync` and `measureAsync` APIs can instrument the actual
call sites in a disposable lab checkout without replacing the operations:

1. Create a capture immediately before navigation, and publish it temporarily as
   `window.__AO_CHAT_OPEN_CAPTURE`. The generated harness does this only during
   its measured open. Clear/cancel the capture after each attempt, including failures.
2. In `hooks/useConversation.ts`, time the existing awaited client GET in
   `fetchConversationPage` with `measureAsync("fetch", ...)`. That client parses
   JSON internally, so this span is **API wait including JSON**, not network-only.
   Record Resource Timing response boundaries separately when available. Leave
   `json` null unless the real parser boundary is separately instrumented; do not
   estimate it by subtracting unrelated durations. Do not issue an extra GET.
3. Wrap the existing `toSnapshot(data)` call in `measureSync("conversion", ...)`.
   Preserve the actual wire response and request's compact option. Hash the
   converted snapshot outside this timing; retain only the checksum/counts.
4. In `components/chat/ChatWorkspace.tsx`, wrap the **existing** grouping `useMemo`
   callback (the `groupByTurn` call and following filter/map) in
   `measureSync("grouping", ...)`. Do not call it a second time, copy its algorithm,
   or move grouping outside React. This excludes the later stable-list comparison.
5. In that same lab only, put a React `<Profiler id="chat-timeline">` around the
   existing `Timeline`, preserving `ChatHostId`, `ActivityDetailProvider`, the
   lazy full-row id/revision checks and image/body gates above/below it. Feed
   `capture.onRender` to the Profiler. Keep that timeline scope separate from the
   synthetic harness's whole-workspace scope. Record render samples and commit
   timestamps; `reactCommitPhaseWallMs` stays null without a validated wall-time probe.
6. Begin a screenshot-free renderer Performance trace before navigation with User
   Timing and renderer timeline events enabled. `measureChatOpen({traceMarks:true})`
   emits a unique `traceMarkPrefix` for start, stage boundaries, React commit and
   visibility observations. Correlate renderer `Paint` events with these marks on
   the correct renderer main thread. Report the event definition/thread/clock
   alignment with any paint interval; a generic Paint is not proof that the final
   message was presented. Never publish raw private trace/screenshots or response bodies.

`documentFirstPaintMs` is the browser's document-load PaintTiming entry, and is
normally null for same-document navigation. It is **not** chat first paint.
`firstVisibleFrameMs` is the first geometry/visibility observation, while
`firstPaintOpportunityMs`/`lastMessageVisibleMs` require two visible frames. That
permits a paint opportunity but does not prove compositor presentation. Keep these
as separate fields; do not rename a frame proxy to measured first paint.

Uninstrumented fetch/conversion/grouping stages are null in generated samples.
Nested stage/Profiler spans overlap; do not add them as independent costs. Retain
all repeated spans/commits rather than choosing the fastest. The original app is
not instrumented by this recipe alone: actual stage and paint results remain
unmeasured until the lab probes and trace are run and validated.

For the real-chat experiment, use an owned sandbox snapshot, the same five
conversation IDs ranked by a recorded size criterion, and exact baseline/head
revisions. Record desktop navigation through final-message visibility separately,
with timeline-scoped profiling and the same launch settings. Compare opened text
privately, publishing only aggregate metrics/checks. Verify scroll/history/stream
flows. The under-400ms and no-over-200ms targets remain proposed until measured.
