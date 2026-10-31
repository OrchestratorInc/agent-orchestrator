// Separate Sentry projects identify errors from the Go daemon and the desktop
// Electron surfaces. The daemon sends only when its existing consent gate allows
// it. Electron main sends consented desktop errors; renderer captures pass
// through the existing main-owned policy boundary. Mobile has its own project and DSN
// in packages/mobile because the Expo bundle cannot reach frontend/src.
// These client DSNs are write-only ingest endpoints, not secrets.
export const DEFAULT_DAEMON_SENTRY_DSN =
	"https://e21b875a3f97a58fa0cf4f6754285267@o4511955966361600.ingest.us.sentry.io/4512334276329472";

export const DEFAULT_ELECTRON_SENTRY_DSN =
	"https://5fe434b5838d57447e2a4b5b4e8b551a@o4511955966361600.ingest.us.sentry.io/4512334283931648";

export function daemonSentryDsn(override: string | undefined, isDev: boolean): string {
	return override ?? (isDev ? "" : DEFAULT_DAEMON_SENTRY_DSN);
}

export const SAFE_FUNCTION = /^[A-Za-z_$][A-Za-z0-9_$.:<>]*$/;

const SAFE_EXCEPTION_TYPES = new Set([
	"Error", "EvalError", "RangeError", "ReferenceError", "SyntaxError", "TypeError", "URIError", "AggregateError", "DOMException",
]);

export function safeSentryExceptionType(value: unknown): string {
	return typeof value === "string" && SAFE_EXCEPTION_TYPES.has(value) ? value : "Error";
}

// Keep this version mapping aligned with sentryEnvironment in the Go daemon.
export function sentryEnvironment(version: string): "unknown" | "nightly" | "development" | "stable" {
	const value = version.trim().toLowerCase();
	if (!value) return "unknown";
	if (value.includes("nightly")) return "nightly";
	if (value.includes("edge") || value.includes("pr")) return "development";
	return "stable";
}
