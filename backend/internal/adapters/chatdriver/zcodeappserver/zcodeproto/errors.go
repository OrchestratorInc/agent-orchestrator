package zcodeproto

// JSON-RPC error codes the app server emits (bundle symbol `bne` and the
// `decodeLine` rejection path), plus the one code AO chooses for its own
// declines.
//
// CodeSessionRuntimeRejected (-32031) is deliberately not named after runtime
// headers: the server throws it for a restoreWarning on session/send and
// session/compact (data.code = restoreWarning.type, symbols `yci`/`vci`), for
// provider runtime headers not applied before a model request (data.providerId),
// and for unsupported background-task cancellation. The driver surfaces its
// message and data verbatim and never maps it to a credentials condition.
const (
	// CodeParseError is answered with id "parse-error" when a line is not JSON.
	CodeParseError = -32700
	// CodeInvalidMessage is answered with id "invalid-message" and
	// data.issues when a frame fails the strict 4-way union schema (`PXt`).
	CodeInvalidMessage = -32600
	// CodeMethodNotFound is the only code (with CodeNoClientAttached) that makes
	// session/requestRuntimePreferences fall back to defaults instead of failing
	// session/create (`Xai`). AO uses it for every request it does not model.
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	// CodeInternal is what session/setModel answers for a selection the
	// registry refuses: "Provider Registry 中不存在 Model" for an unknown provider
	// and ModelProtocolError "Reasoning level is required for <provider>/<model>"
	// when options.reasoningLevel is missing (PROVEN, p7-models-level-required.ndjson).
	CodeInternal = -32603
	// CodeSessionUnavailable carries two distinct messages: "Session not found:
	// <id>" (`DFn`, unknown id on session/resume; PROVEN, p8-resume.ndjson) and
	// "Session is not active: <id>" (`Np`, resident session evicted; STATIC).
	// Callers branch on the message prefix, never on the code alone (U7).
	CodeSessionUnavailable = -32004
	// CodeRevisionMismatch is raised when an expectedRevision does not match;
	// the driver never sends one.
	CodeRevisionMismatch = -32009
	// CodePromptConflict is "A prompt is already running for this session"
	// (`yci`), thrown before prompt_started is emitted. PROVEN
	// (p1-create-send-complete.ndjson: a second session/send during a turn).
	CodePromptConflict         = -32010
	CodeNoClientAttached       = -32020
	CodeClientCancelled        = -32021
	CodeClientTimedOut         = -32022
	CodeSessionRuntimeRejected = -32031
	// CodeClientDeclined is AO-chosen for interaction/requestOfficialMcpAuthHeaders:
	// the server's `Koi.resolveHeaders` catch-all maps any error to
	// {ok:false, reason:"official_auth_unavailable"}, so the code is free.
	CodeClientDeclined = -32001
)

// SessionNotFoundPrefix is the message prefix of a CodeSessionUnavailable error
// for an id the server has no record of (`DFn`). PROVEN (p8-resume.ndjson).
const SessionNotFoundPrefix = "Session not found:"

// SessionNotActivePrefix is the message prefix of a CodeSessionUnavailable error
// for a resident session the server evicted (`Np`). STATIC, U7.
const SessionNotActivePrefix = "Session is not active:"
