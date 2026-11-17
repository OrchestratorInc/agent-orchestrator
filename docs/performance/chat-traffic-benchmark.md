# Chat traffic benchmark

A local, deterministic measurement of the network traffic behind an
Orchestrator chat: the pages a client reads when it opens a chat or scrolls
back, and the refreshes while a tool-heavy reply streams. It compares a
baseline commit with the current working tree and checks both against reviewed
limits.

It is local-only. Nothing is published, no provider runs, no account or real
transcript is read, and nothing listens beyond `127.0.0.1`.

## One command

```bash
scripts/chat-traffic-benchmark.sh                       # baseline: origin/main (else main), no fetch
scripts/chat-traffic-benchmark.sh --baseline-ref <ref>  # any commit
scripts/chat-traffic-benchmark.sh --out-dir /tmp/tb     # where run, report and log files go
```

It prints the Markdown report and writes `baseline.json`, `candidate.json`,
`report.json`, `report.md`, and both harness logs to the output directory
(default `$TMPDIR/ao-chattraffic-results/<UTC time>`). Relative `--out-dir`
and `--limits` paths are taken from the directory you run it in. Each run
first deletes those files from the output directory, so a reused directory
never hands an earlier run's results to this one.

Exit codes: `0` pass, `1` a limit, relative cap, or semantic check failed,
`2` invalid input, incomparable runs, a failed replay, or setup failure.

To replay the harness alone, as an ordinary test:

```bash
cd backend
AO_CHATTRAFFIC=1 GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOPROXY=off \
  go test -count=1 -p 1 -run '^TestChatTrafficBenchmark$' ./internal/httpd/
```

Without `AO_CHATTRAFFIC=1` (or the script's variables) the test skips, so
`go test ./...` does not pay for the replay.

For analysis, `AO_CHATTRAFFIC_DUMP_DIR=<dir>` also writes every counted
request with its decoded body, every event frame, each stream-reply step, and how
each refresh resolved (local refetch, merged tail, tail then full page) as JSON
lines, one file per scenario. It never changes a measurement.

## What it measures, mapped to the real protocol

An Orchestrator chat client reads its conversation as paged JSON. The CDC
event stream only tells it that something changed, and the client then reads
again:

| Path | Transport in AO | In this benchmark |
|---|---|---|
| Conversation page | `GET /api/v1/sessions/{id}/conversation` (`controllers/conversations.go` `snapshot`), paged by `beforeSequence`/`afterSequence`/`limit`, remote output cut by `outputLimit` | Actual handler, service, store, DTO and `envelope.WriteJSON` |
| Change signal | SQLite triggers write `session_updated` rows to `change_log` (payload `id`, `sessionId`, `conversationId`, `activity`, …); `cdc.Poller` publishes; `GET /api/v1/events` (`httpd/events.go`) writes `id`/`event`/`data` SSE frames | Actual triggers, poller, broadcaster and SSE writer |
| Refresh | Renderer reads again. Local: refetches every loaded page, or, with the local tail refresh, reads the rows above the live page's floor (`afterSequence=floor&limit=30`, whole outputs) and merges them, falling back to the full refetch. Remote: reads a tail (`afterSequence=floor&limit=30&outputLimit=512`) and merges it (`useConversation.ts` `refreshRemoteConversation`) | Go **simulator** of the renderer's rules, applied to real responses |
| Local desktop | Loopback listener, HTTP/1.1, never compressed | `local`: the production router on a loopback `httptest` server |
| Remote desktop and mobile | LAN listener chain `lanControlBlock(auth(remoteJSONCompression(router)))`: bearer auth, gzip or zlib-deflate for `application/json` only; SSE never compressed | `remote-lan`: that exact handler (`NewMobileLAN(...).handler`) on a loopback `httptest` server, with `Accept-Encoding: gzip, deflate, br, zstd` |
| Remote over a tunnel | `/api/v1/tunnel` carries HTTP/2 inside one binary WebSocket with compression disabled on both ends (`lan_tunnel.go`, `remote-proxy.ts`), the same LAN chain inside; or cloudflared TLS | **Not measured.** Inner request bodies are the `remote-lan` bodies; HTTP/2, WebSocket, TLS and TCP framing are excluded. No WebSocket compression exists to model. |

### The measured seam

```
fixture steps (normalized ports.ChatEvent)
  -> benchConversation.Events()            (stands where a persistent Codex/ACP host stands)
  -> chat Controller: pumpProviderEvents, projectUnit, provider-event acknowledgement
  -> SQLite store (production migrations) + change_log triggers
  -> lifecycle.Manager activity reduction  (session activity rows and their CDC)
  -> cdc.Poller.Poll -> cdc.Broadcaster -> EventsController SSE  ----\
  -> ConversationsController.snapshot -> chat Service.SnapshotPage  --+-> router [-> LAN chain] -> loopback socket
                                                                       -> simulated renderer
```

The seam starts after the provider adapters have turned native frames into
normalized chat events, so adapter parsing is not part of the measurement. Codex
app-server JSON-RPC parsing, ACP parsing, and the persistent provider host
process are not exercised. The fixtures follow what those adapters emit (see
Fixtures).

The harness mirrors the daemon's wiring where it cannot call it: the chat
service's `Reader`/`PageReader` are the daemon's adapters reproduced by copying
every `chatsvc.ConversationRows` field from the store snapshot field of the same
name, failing if one is missing, so the harness cannot serve smaller pages than
the daemon.

## Client actions

Each scenario is one provider, one client action (the `phase` field) and one
transport. Page sizes are read from the renderer source at each revision and
recorded in the run's `clientPolicy`; the values below are today's.

| Action | What the client does | Requests |
|---|---|---|
| `open-chat` | Open the chat: newest page (`limit=30` local; `limit=30&outputLimit=512` remote). `limit` counts messages; the activities between them ride along within the store's page byte limit | 1 |
| `scroll-to-top` | Open, then load older pages until `hasMoreBefore` is false (`limit=200` local; `limit=100&outputLimit=512` remote) | 1 + older pages |
| `stream-reply` | Chat already open (not counted). Connect the event stream at `after=latest`, `POST` the user message, then one refresh cycle per committed step of a tool-heavy turn | POST + refreshes |
| `expand-output` (remote only) | Expand every tool output the remote page cut, as `fetchActivityOutput` does (`limit=1` page without `outputLimit`). An upper bound: a reader expands only what they open | one per cut output |

Each fixture has 60 settled turns (140 Codex and 120 Claude messages), sized
so an opening page holds only part of the history and scrolling to the top
pages; the harness fails if a remote open page holds everything. The reply
turn adds reasoning, streamed assistant text, several commands with long
output, file edits, and a tool call.

## Metrics

All counted at the simulated client's TCP sockets over loopback.

| Metric | Meaning |
|---|---|
| `requests` | Conversation HTTP requests (the stream connection is not one) |
| `cdcRows` | `change_log` rows the reply turn produced |
| `sseFrames` | Event-stream frames received, including the initial cursor frame |
| `sseBytes` | Event-stream body after de-chunking (never compressed) |
| `headerBytes` | Response status lines and headers |
| `chunkBytes` | HTTP/1.1 chunked-encoding overhead |
| `bodyBytes` | JSON response bodies as received: gzip on `remote-lan`, plain on `local` |
| `jsonBytes` | The same bodies after decompression: the JSON the client parses |
| `downloadBytes` | Everything the client read: `headerBytes + chunkBytes + bodyBytes + sseBytes`, checked as that exact sum |
| `uploadBytes` | Everything the client sent: request lines, headers, the POST body |

### Actual vs modeled

Actual: every response byte is produced by production code at the measured
commit, including gzip by the daemon's own middleware, and HTTP/1.1 framing as
Go's server writes it. `jsonBytes` comes from real decompression.

Modeled: when the client asks (the request schedule), request headers (Go's
client plus the renderer's `Accept`, `Accept-Encoding` and bearer headers, not
Chromium's full header set), and everything outside the socket (TLS, TCP/IP,
HTTP/2, WebSocket). No compression is modeled.

### Scheduled replay, not wall-clock timing

The product coalesces by time: the projector batches same-stream deltas for up
to 50 ms, and the renderer batches invalidations in a 150 ms window with one
refresh in flight. Wall-clock coalescing would make counts vary run to run, so
the benchmark fixes a schedule:

- Each fixture step is one event and one projector unit. The controller
  acknowledges an event before its after-commit work (activity reporting, turn
  lifecycle), which can still write rows, so the harness then delivers the same
  event again: the archive skips it as a replay and writes nothing, and its
  acknowledgement can only arrive after the first delivery's unit has fully
  returned. A row written after that barrier fails the run.
- After each step the harness polls CDC once, the client reads exactly the
  frames up to the newest `change_log` sequence, applies the renderer's routing,
  and performs at most one refresh.
- Heartbeat frames are disabled; the service clock is injected and advances one
  whole second per step; ids are UUID-shaped and deterministic; session
  timestamps written by the lifecycle reducer, which has no exported clock, are
  truncated to whole seconds so RFC 3339 lengths do not vary.

Request and frame counts are therefore "scheduled replay" counts: one refresh
per committed step is the worst case for a client whose round trip is shorter
than the provider's gap between units. Real coalescing can only lower them.

## Semantic validation

Bytes cannot pass by losing content. Each scenario reduces the client's
reconstructed view and a fresh, unpaged server read over the same window to a
canonical text: ordered rows and the states of the turns they belong to. Every
row starts with the index of its turn in that list, so a row that moves to
another turn changes the text. A message row then holds its role and the sha256
and size of its text. An activity row holds its kind and status, and four
values:

- the sha256 of its summary plus settled detail text;
- its full output size;
- the sha256 of its tool payload: the detail without `output`, `outputBytes`
  and `text`, as compact sorted JSON. This covers file changes and patches,
  content blocks, inputs, arguments and results;
- the sha256 of the output the reader holds.

On `remote-lan` that last value is the head-and-tail preview a page keeps of a
cut output. The client view is reduced exactly as received. The server read
holds whole outputs, so it is reduced with `chattraffic.OutputPreview`, an
independent statement of the documented `outputLimit` rule. A missing, empty or
wrong preview therefore differs. Then:

- the server text, with whole outputs, must match the fixture-only oracle that
  `scripts/chat-traffic-oracle.py` derives without running the product
  (`testdata/expected/oracle-*.txt`): the whole history and every cut output
  exactly, and the reply turn's rows as the view's final rows. Both sides apply
  the daemon's documented display rule for streamed command output
  (`activityDetailPayload`). The streamed text is served as `output`, with
  `outputSource: "stream"` and `outputMayBePartial: true`. The oracle is an
  independent Python re-implementation of the fixture rules, so an expected
  state can never be blessed from product output alone;
- the server text must equal the committed expected state
  (`backend/internal/chattraffic/testdata/expected/*.txt`);
- the client text must equal the server text;
- every message and activity the fixture produces must appear (whole history,
  or the reply turn when the view is a bounded page). Each must match the
  fixture's tool payload, and the output or remote preview derived from the
  fixture. Every full output in the view must be one the fixture produced;
- cut remote outputs must report `outputBytes` equal to the full size, and
  expanding them must return exactly the fixture output.

A mismatch poisons the run's `clientDigest`, so a comparison reports
`candidate-state-mismatch` (exit 1) even if bytes fell. Run alone, the harness
fails on it. With an output path, it logs the difference and lets the artifact
carry it.

Any other harness failure means the replay itself broke an invariant. Examples:
SSE frames not equal to change rows plus the cursor frame, a row written after
a step barrier, a remote history that fits one page, or an expanded output of
the wrong size. In that case the harness writes no artifact. The runner treats
any non-zero harness exit, or a missing artifact, as an invalid run (exit 2),
so a broken replay can never pass.

Expected states change only when regenerated explicitly with
`AO_CHATTRAFFIC_UPDATE_EXPECTED=1`, never by the runner.

## Baseline and candidate identity

- The baseline is `git archive <sha>` of `backend/` and the two renderer files,
  extracted to `$TMPDIR/ao-chattraffic-baseline/<sha>`. No fetch, no worktree
  registration, the source checkout untouched.
- Only harness files are copied in: `backend/internal/chattraffic/**` and
  `backend/internal/httpd/chattraffic_harness_test.go`. The overlay only adds;
  a baseline that already has a different harness file is refused, never
  shadowed.
- `verify-export` then checks every file in the baseline tree against the
  commit's git blob id, and that nothing but the overlay was added.
- Both sides run with `GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly
  GOPROXY=off`, `-count=1 -p 1`, and the runner asserts each side compiles its
  own module directory.
- Each run records the source commit, a digest of uncommitted changes to the
  measured paths, Go version and platform, digests of the harness, fixtures plus
  expected states, the limits file, and the renderer source, the renderer
  policy constants it applied, and a fresh nonce.
- The candidate's commit and uncommitted-change digest are checked again after
  its replay. A source edited while the runs compiled ends the run with exit 2
  instead of being measured under the identity recorded before.
- Runs of the same baseline commit share its exported tree, so a run holds a
  lock on it (`<tree>.lock`, holding its pid). A second run is refused while the
  first is alive, and a lock left by a run that died is taken over.

## Comparison rules

`backend/internal/chattraffic` uses only the standard library. It reads run
and limits files as plain data and rejects anything unexpected: a file over
256 KiB or not a regular file, a byte-order mark or trailing data, duplicate,
unknown, missing or case-variant keys, numbers other than plain non-negative
integers up to 10^9, ids outside the provider, action and transport lists, and
a `downloadBytes` that is not the sum of its parts.

Incomparable (exit 2): different harness, fixtures, toolchain or limits
digest; a dirty baseline; refs other than requested; a missing scenario or
limit row; differing expected states; a baseline that did not reproduce its
expected state; or renderer source that differs from what the simulator models.
`--allow-client-change` shows the numbers for such a pair, but the report is
labeled unverified and exits 2 unless something failed: it is never a pass. No
relative change is reported for an incomparable pair.

Fail (exit 1): any candidate metric above its absolute limit (equal passes),
above `baseline + baseline × maxIncreasePermille / 1000` (integer arithmetic),
non-zero where the baseline had zero, or any semantic mismatch.

Limits live only in `backend/internal/chattraffic/limits.json`, reviewed
like code, bound to one fixture set by digest. A run never writes them and none
is derived from a baseline at run time.

## Fixtures

`backend/internal/chattraffic/testdata/fixtures/{codex,claude}.json` are
scripts of normalized events with invented content for a fictional project.

- Codex shapes follow `codexappserver/normalize.go` (`activityFor`,
  `activityDetail`, `encodeDetail`): reasoning items, aggregate command output
  with `outputSource`, `files` patches, MCP `server`/`toolName`/`arguments`.
- Claude shapes follow the ACP adapter (`acp/client.go` `toolEvent`): every tool
  update re-emits the whole accumulated detail (`protocol`, `toolKind`,
  `locations`, `input`, `output`, `content`, `providerToolName`); edits carry
  `patch`, `oldText`, `newText` and diff content. Tool content blocks are
  representative of claude-code-acp, not captured frames.

## Limitations

- Provider adapters and persistent hosts are not exercised; fixture realism is
  only as good as the shapes above.
- The renderer is simulated in Go from `useConversation.ts` and
  `event-transport.ts` at the commit in `simulatorClientSHA256`. If those files
  change, runs are incomparable until the simulator is reviewed and its binding
  updated.
- Only the conversation is measured. Workspace and session-list refetches that
  the same events trigger are not.
- Timing, latency, CPU and memory are not measured.
- Byte counts are exact for this machine and Go version; gzip output depends on
  the Go version, so runs from different toolchains are incomparable.

## Not in CI

The benchmark is a local tool; no workflow runs it. If one ever does, it should
compare with `main`'s copy of this package and `main`'s limits file, and read a
branch's run files only as data: never executed or sourced, and rendered only
as allowlisted ids and numbers. The harness reports its own numbers, so a check
built on them is only as good as the review of the harness, fixtures and
limits.
