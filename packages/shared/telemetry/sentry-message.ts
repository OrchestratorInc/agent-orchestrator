import { safeSentryCode } from "./ao-error";

const ERROR_PREFIX = /^(?:Uncaught(?: \(in promise\))? )?(?:[A-Za-z_$][\w$]*)?Error:\s+|^Uncaught(?: \(in promise\))? /;
const IPC_WRAPPER = /^Error invoking remote method '([a-z][\w:-]{0,63})':\s*/;
const ENGINE_TYPES = new Set(["TypeError", "ReferenceError", "RangeError", "SyntaxError", "EvalError", "URIError", "DOMException"]);
// V8 interpolates the runtime value or property name without quotes.
const UNQUOTED_ENGINE_VALUE = /^(?:Cannot convert .+ to a BigInt|Cannot add property .+, object is not extensible)$/;
const INNER_TYPE = /^(\w+):\s+/;
// An unquoted path may span words. Keep consuming a segment if a later word
// contains a separator, stopping at the start of another absolute path.
const ABSOLUTE_PATH = /(?:[A-Za-z]:[\\/]|\\\\|~\/|(?<![\w/])\/)(?:\\[ \t]|[^\s"'‘’“”<>\x60])+(?:(?:[ \t]+[^\s\\/:"'‘’“”<>\x60]+)*[ \t]+(?![\\/~]|[A-Za-z]:)[^\s\\/:"'‘’“”<>\x60]+[\\/](?:\\[ \t]|[^\s"'‘’“”<>\x60])+)*/g;

function normalizeSentryMessage(value: unknown): { text: string; unsafe: boolean; channel?: string; innerType?: string } {
	if (typeof value !== "string" || !value || value.length > 4096) return { text: "", unsafe: false };
	const line = value.split(/[\r\n\u2028\u2029]/, 1)[0];
	let message = line.trim().replace(ERROR_PREFIX, "");
	const ipc = IPC_WRAPPER.exec(message);
	if (ipc) message = message.slice(ipc[0].length);
	const innerType = ipc ? INNER_TYPE.exec(message)?.[1] : undefined;
	if (ipc) message = message.replace(ERROR_PREFIX, "");
	const text = message
		.replace(/\b(?:(?:https?|app|wss?):\/\/|file:\/)[^\s"'‘’“”<>\x60]+/gi, "<url>")
		.replace(/[\w.!#$%&*+/=?^{}|~-]+@[\w-]+(?:\.[\w-]+)+/g, "<email>")
		.replace(ABSOLUTE_PATH, "<path>")
		.replace(/"(?:\\.|[^"\\])*(?:"|$)|(?<![\p{L}\p{N}])'(?:\\.|[^'\\]|'(?=\p{L}))*?(?:'(?!\p{L})|$)|\x60(?:\\.|[^\x60\\])*(?:\x60|$)|“[^”]*(?:”|$)|‘[^’]*(?:’|$)/gu, "'?'")
		.replace(/\b[\w-]*(?:password|token|api[_-]?key|authorization|secret)\s*[:=].*/gi, "<credential>")
		.replace(/\bbearer\s+[\w./+=-]+/gi, "<credential>")
		.replace(/\b(?:prompt|conversation|source code)\s*[:=].*/gi, "<content>")
		.replace(/\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b/gi, "<id>")
		.replace(/\b(?=[a-z0-9_]{8,}\b)(?=[a-z0-9_]*[a-z])(?=[a-z0-9_]*\d)(?!0x[0-9a-f]+\b)[a-z0-9_]+\b/gi, "<id>")
		.replace(/\b0x[0-9a-f]+\b/gi, "<n>")
		.replace(/(?:(?<![\w>-])[+-])?\d+(?:[.,]\d+)*/g, "<n>")
		.replace(/(?:<id>|<n>|[\w-]+)(?:<n>[\w-]*)*-<n>(?![\w-])/g, "<id>")
		.replace(/\s+/g, " ")
		.trim();
	return { text, unsafe: line.length > 300 || text.split(" ").length > 25, channel: ipc?.[1], innerType };
}

function canDisplayEngineMessage(value: string): boolean {
	const line = value.split(/[\r\n\u2028\u2029]/, 1)[0];
	// Nested quotes in engine snippets can leave private text between redactions.
	if ((line.match(/["“”]/g)?.length ?? 0) > 2) return false;
	const message = line.trim().replace(ERROR_PREFIX, "").replace(IPC_WRAPPER, "").replace(ERROR_PREFIX, "");
	return !UNQUOTED_ENGINE_VALUE.test(message);
}

export function sentryMessageIdentity(value: unknown, errorCode?: unknown, exceptionType?: string) {
	const { text, unsafe, channel, innerType } = normalizeSentryMessage(value);
	const stableCode = safeSentryCode(errorCode);
	const engineType = channel ? innerType : exceptionType;
	const canDisplay = !stableCode && !unsafe && !!text && typeof value === "string"
		&& ENGINE_TYPES.has(engineType ?? "") && canDisplayEngineMessage(value);
	return {
		code: stableCode,
		channel,
		label: stableCode ?? "",
		text: canDisplay ? text.slice(0, 120) : undefined,
		// Display eligibility does not affect grouping.
		hashInput: !stableCode && text ? text.toLowerCase() : undefined,
	};
}

export function sentryIdentityTitle(identity: ReturnType<typeof sentryMessageIdentity>, fallback: string): string {
	if (!identity.label) return identity.text ?? fallback;
	if (!fallback) return identity.label;
	return fallback.endsWith(" (" + identity.label + ")") ? fallback : fallback + " (" + identity.label + ")";
}

export function sentryExceptionValue(message: string, title: string, location: string, label: string): string {
	if (!message) return title.slice(0, 200);
	const suffix = ` (in ${location}, ${label})`.slice(0, 199);
	return message.slice(0, 200 - suffix.length) + suffix;
}

const SAFE_EXCEPTION_TYPES = new Set([
	"Error", "EvalError", "RangeError", "ReferenceError", "SyntaxError", "TypeError", "URIError", "AggregateError", "DOMException",
	"ApiError", "ArtifactTextTooLargeError", "ArtifactTooLargeError", "AuthStoreUnreadableError",
	"BrowserProfileStoreError", "CloudAuthError", "CloudCpAuthError", "CloudCpError", "CloudError",
	"CloudShareError", "EndpointRefreshError", "FolderPushError", "GitHubDaemonError",
	"IncompatibleHostVersionError", "IncompatibleRemoteVersionError", "LocalAuthError",
	"OrchestratorSpawnError", "TaskCreateError", "WorkspaceRequestError",
]);

export function safeSentryExceptionType(value: unknown): string {
	return typeof value === "string" && SAFE_EXCEPTION_TYPES.has(value) ? value : "Error";
}
