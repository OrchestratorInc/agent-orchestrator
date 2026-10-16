# MiniMax Code Chat binding

The candidate `minimaxacp` binding uses MiniMax Code's first-party `mcode acp`
server and the existing AO ACP transport, persistent host, conversation API and
Chat renderer. It is deliberately **unregistered** until independent native
Chat conformance passes. Existing TUI qualification and failures remain in
[the TUI record](../research/minimax-code-tui.md); they do not qualify Chat or
TUI-to-Chat handoff. Reviewer and interface handoff are outside this change.

## Native contract

The binding pins the existing **0.6.5** installation. Its official
[npm distribution](https://registry.npmjs.org/@minimax-ai/code/-/code-0.6.5.tgz)
contains `chunks/run-acp-command-4JFNMYRS.js` and documents `mcode acp` in the
bundled README. Archive SHA-256:
`de35e828f47a29426f169be16fece70eaf455b222d61d044d2e134e5759f1217`.
The [release source](https://github.com/MiniMax-AI/minimax-code/tree/309e28554ec091bb17b6c4f67111c8c01d688b94/packages/tui/src/acp)
and bundled executable agree on these wire contracts:

- ACP v1, agent name `minimax-code`, `session/load`, streamed message/tool updates,
  `session/request_permission` and `session/cancel`.
- `session/load` rejects a missing native ID or a mismatched persisted workspace.
  AO preserves the exact `mvs_` identity and disables fresh-session fallback.
- Session modes `default` and `plan` describe planning. Permission policy instead
  uses the **`permissionMode`** select configuration option with values `default`,
  `auto` and `bypassPermissions`. AO rejects accept-edits and unknown policies;
  native offered allow/reject decisions reach the shared approval UI unchanged.
- **`model`** options use `m:<encoded-provider>:<encoded-model>:u` or a native
  variant suffix. Prelaunch `provider/model` IDs resolve against the live
  advertised catalog with JavaScript's `encodeURIComponent` semantics. The
  matching current native variant is preserved, including an explicit empty
  variant (`:v:`); otherwise the model must offer a unique choice. Ambiguous
  variants require an explicit native selection. Native selections remain opaque.
  **`thinkingEffort`** is the native effort option. AO requires an advertised
  option/value before sending it; it invents no models or effort levels.
- Images and embedded resources are unsupported. Steering, compaction and
  rollback are not enabled by this binding.

Chat owns a separate profile under
`AO_DATA_DIR/agents/minimax-code/chat/<session-id>`. It snapshots bounded native
configuration, auth, global instructions and custom resources without importing
TUI history or AO terminal hooks. Each replacement process refreshes source auth
and provider configuration while retaining its native history. The source is
read only; unowned profiles and symlinked resources fail explicitly. Platform
`TMPDIR`/`TMP`/`TEMP` remain unchanged to avoid long Unix socket paths.

The process receives a bounded private 0600 file via
`--append-system-prompt-file`; native defaults remain intact, and the context
is never sent as a user transcript turn. Each new process receives a fresh file,
including exact restore. **Fresh instruction delivery after native load remains
a mandatory unproven gate**: the historical TUI append route failed that case.
An initialize declaration cannot establish this property.

## Credential-free native conformance

Install official `@minimax-ai/code@0.6.5` with its optional dependencies and Node
24 on Linux. Then, on the authorized execution host:

```bash
cd backend
AO_NATIVE_MINIMAX_ACP=1 go test -count=1 -v -timeout=8m \
  ./internal/adapters/chatdriver/minimaxacp \
  -run '^TestNativeMiniMaxACPConformance$'
```

The opt-in test launches the actual native executable and AO detached host with
isolated profile/workspace/data. Only the model endpoint is scripted loopback
Anthropic SSE; no provider credentials are needed. It checks streaming, offered
approval/rejection and side effects, executing-tool PID/kernel-start cancellation
with no delayed artifact, same-ID replacement-process load and native replay,
fresh private context in the actual post-load model request, and missing-ID/cwd
refusal. Default unit tests do not execute the native binary or contact a provider.
The dedicated CI workflow runs this native test; ordinary backend CI owns unit
and service regression suites.

A passing controlled endpoint test establishes those native/protocol cases only.
Real account authorization, independent model instruction consumption, registered
AO API lifecycle/recovery, actual shared Chat UI journeys and screenshots still
need separate qualification. Do not describe Chat as production supported or
register it solely from source review, initialize metadata, or green unit tests.
