export const DEFAULT_DAEMON_SENTRY_DSN =
	"https://e21b875a3f97a58fa0cf4f6754285267@o4511955966361600.ingest.us.sentry.io/4512334276329472";

export const DEFAULT_ELECTRON_SENTRY_DSN =
	"https://5fe434b5838d57447e2a4b5b4e8b551a@o4511955966361600.ingest.us.sentry.io/4512334283931648";

const PUBLISHED_VERSION = /^\d+\.\d+\.\d+(?:-nightly\.\d{12})?$/;

export function isPublishedSentryBuild(version: string, isDev: boolean): boolean {
	return !isDev && process.env.AO_PUBLISHED_BUILD === "1" && PUBLISHED_VERSION.test(version.trim());
}

export function daemonSentryDsn(override: string | undefined, version: string, isDev: boolean): string {
	return override ?? (isPublishedSentryBuild(version, isDev) ? DEFAULT_DAEMON_SENTRY_DSN : "");
}

export const SAFE_FUNCTION = /^[A-Za-z_$][A-Za-z0-9_$.:<>]*$/;
export const SAFE_APP_ASSET = /^assets\/[A-Za-z0-9_][A-Za-z0-9_.-]{0,80}-[A-Za-z0-9_-]{8,16}\.(?:js|mjs)$/;

const SAFE_FUNCTION_NAMES = new Set([
	"ChatWorkspace", "ChatWorkspaceContent", "SessionView", "LazyChunkBoundary",
	"TelemetryBoundary", "bindRendererErrorHandlers", "captureRendererException",
	"initMainSentry", "startDaemon", "startDaemonInner", "startDaemonForRestart",
]);

export function safeSentryFunctionName(value: unknown, fallback: string): string {
	return typeof value === "string" && SAFE_FUNCTION_NAMES.has(value) ? value : fallback;
}

const SAFE_EXCEPTION_TYPES = new Set([
	"Error", "EvalError", "RangeError", "ReferenceError", "SyntaxError", "TypeError", "URIError", "AggregateError", "DOMException",
]);

export function safeSentryExceptionType(value: unknown): string {
	return typeof value === "string" && SAFE_EXCEPTION_TYPES.has(value) ? value : "Error";
}

export function sentryEnvironment(version: string): "unknown" | "nightly" | "development" | "stable" {
	const value = version.trim().toLowerCase();
	if (!value) return "unknown";
	if (value.includes("nightly")) return "nightly";
	if (value.includes("edge") || value.includes("pr")) return "development";
	return "stable";
}
