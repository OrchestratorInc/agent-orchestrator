# Large-chat measurement workloads

These opt-in workloads use real renderer components with deterministic synthetic
data. They do **not** measure navigation through the desktop shell or replace the
five-real-chat before/after measurements required by #7259.

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

For the real-chat experiment, use an owned sandbox snapshot, the same five
conversation IDs ranked by a recorded size criterion, and exact baseline/head
revisions. Record desktop navigation through final-message visibility separately,
with timeline-scoped profiling and the same launch settings. Compare opened text
privately, publishing only aggregate metrics/checks. Verify scroll/history/stream
flows. The under-400ms and no-over-200ms targets remain proposed until measured.
