// Package zcodeappserver drives ZCode through `zcode app-server --stdio` and
// normalizes its notifications into AO's provider-neutral Chat events. It is
// the persistent conversational adapter for the zcode harness; the one-shot
// `-p` TUI adapter in adapters/agent/zcode stays as it is.
//
// Transport (DESIGN-zcodeappserver.md 3.3). The wire is NDJSON validated by
// the server against a strict 4-way union: request {id, method, params?},
// response {id, result|error}, notification {method, params?}, and server->client
// request {id:"server-<n>", method, params}. There is no "jsonrpc" field, so
// codex's rpc.go is reused with two deltas: protocol-level rejections
// ({id:"parse-error"} / {id:"invalid-message", error.data.issues}) are logged
// at Error because they mean AO wrote a frame the strict schema refused, and a
// handler may choose its own refusal code (-32601 for anything AO does not
// model, the only code that lets session/requestRuntimePreferences fall back
// to defaults). Our ids are an int64 counter seeded from the persistent host on
// reconnect; the server re-announces an unanswered interaction request under a
// fresh server-<n> id every 1-10 s and every one of those ids is answered.
//
// Deferred turns (3.6). The session/send handler emits
// state.updated{reason:"prompt_started"} synchronously BEFORE it returns the
// {accepted} result, and the ack carries no turn id. SendTurn therefore only
// mints the ProviderTurnID; the controller binds it and calls StartDeferredTurn,
// which arms the turn tracker and only then writes session/send. Any other
// ordering lets the first notification reach the pump with no pending id.
//
// Standing instructions (4.3). zcode has no system-prompt channel on
// session/create or session/send. The ACP preamble pattern is used: the role
// text is prepended to the first prompt that actually reaches the provider and
// skipped on a resume that already has turns, byte-for-byte what the TUI
// adapter does for -p, so a -c restore sees the same first turn.
//
// Provenance. Everything in zcodeproto is tagged PROVEN (observed on the wire
// during the Phase 1 probes; the frames live in testdata/*.ndjson, extracted
// from the redacted captures in probes/captures/) or STATIC (read from the
// minified bundle, never exercised). The advertised capabilities are exactly
// the PROVEN set; STATIC behaviour stays behind fail-closed branches (the
// runtimeHeadersResolver decline, the message-prefix branch on -32004, the
// text-source latch). Nothing here guesses.
//
// Credentials. The provider credential is an API key in an AO-owned personal
// provider config that the agent plugin points the CLI at through
// ZCODE_PERSONAL_PROVIDER_CONFIG_FILE (or a one-hour OAuth token in the
// AO-owned store, the legacy route). This package never reads, logs, prints or
// embeds either: AuthStatus is consulted through the agent plugin, the store
// and config are pinned through AugmentRuntimeLaunchEnv, and stderr (where the
// CLI's token logger writes) is discarded.
//
// Cost. session/create.mcpServers replaces the operator's global MCP server
// list for the session (DESIGN-zcode-context-trim.md); without it every turn
// carried ~270k input tokens of tool schema in the Phase 1 runs.
package zcodeappserver
