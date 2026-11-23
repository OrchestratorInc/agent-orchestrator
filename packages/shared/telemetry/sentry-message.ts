import { safeSentryCode } from "./ao-error";

const ERROR_PREFIX = /^(?:Uncaught(?: \(in promise\))? )?(?:[A-Za-z_$][\w$]*)?Error:\s+|^Uncaught(?: \(in promise\))? /;
const IPC_WRAPPER = /^Error invoking remote method '([a-z][\w:-]{0,63})':\s*/;
// An unquoted path may span words. Keep consuming a segment if a later word
// contains a separator, stopping at the start of another absolute path.
const ABSOLUTE_PATH = /(?:[A-Za-z]:[\\/]|\\\\|~\/|(?<![\w/])\/)(?:\\[ \t]|[^\s"'‘’“”<>\x60])+(?:(?:[ \t]+[^\s\\/:"'‘’“”<>\x60]+)*[ \t]+(?![\\/~]|[A-Za-z]:)[^\s\\/:"'‘’“”<>\x60]+[\\/](?:\\[ \t]|[^\s"'‘’“”<>\x60])+)*/g;

function normalizeSentryMessage(value: unknown): { text: string; unsafe: boolean; channel?: string } {
	if (typeof value !== "string" || !value || value.length > 4096) return { text: "", unsafe: false };
	const line = value.split(/[\r\n\u2028\u2029]/, 1)[0];
	let message = line.trim().replace(ERROR_PREFIX, "");
	const ipc = IPC_WRAPPER.exec(message);
	if (ipc) message = message.slice(ipc[0].length).replace(ERROR_PREFIX, "");
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
	return { text, unsafe: line.length > 300 || text.split(" ").length > 25, channel: ipc?.[1] };
}

export function scrubSentryMessage(value: unknown): string {
	const { text, unsafe } = normalizeSentryMessage(value);
	return unsafe ? "" : text.slice(0, 120);
}

export function sentryMessageFingerprint(message: string): string[] {
	return message ? [message.toLowerCase()] : [];
}

export function sentryMessageIdentity(value: unknown, errorCode?: unknown) {
	const { text, channel } = normalizeSentryMessage(value);
	const stableCode = safeSentryCode(errorCode);
	return {
		code: stableCode,
		channel,
		label: stableCode ?? "",
		// Normalization stabilizes grouping. Arbitrary error text stays local.
		hashInput: !stableCode && text ? text.toLowerCase() : undefined,
	};
}

export function sentryIdentityTitle(identity: ReturnType<typeof sentryMessageIdentity>, fallback: string): string {
	return identity.label || fallback;
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
