import { createHmac, randomBytes } from "node:crypto";
import { appendFileSync, chmodSync, mkdirSync, renameSync, statSync } from "node:fs";
import path from "node:path";

export type BrowserCloseFailure = {
	error: unknown;
	sessionId: string;
	tabId: string;
	tabCount: number;
	tabPresent: boolean;
	wasActive: boolean;
	agentTab: boolean;
};

const ERROR_CODES = new Set([
	"BROWSER_PROFILE_SWITCHING", "BROWSER_TARGET_UNAVAILABLE", "BROWSER_TARGET_MISMATCH",
	"TAB_NOT_FOUND", "AGENT_BROWSER_COMMAND_FAILED", "AGENT_BROWSER_START_FAILED",
	"AGENT_BROWSER_TIMEOUT", "AGENT_BROWSER_RUNTIME_CLOSED", "AGENT_BROWSER_SESSION_CLOSING",
	"AGENT_BROWSER_NOT_INSTALLED", "AGENT_BROWSER_INVALID_OUTPUT", "AGENT_BROWSER_OUTPUT_TOO_LARGE",
]);
const ERROR_TYPES = new Set(["Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError", "AbortError"]);
export const BROWSER_CLOSE_LOG_MAX_BYTES = 256 * 1024;

/** Local-only, bounded diagnostics. Never serialize an exception or page metadata. */
export function createBrowserCloseDiagnostics(stateDir: string, appVersion: string) {
	// Per-host keyed IDs correlate failures without storing session names or
	// permitting dictionary attacks against predictable session/tab identifiers.
	const key = randomBytes(32);
	const opaque = (value: string) => createHmac("sha256", key).update(value).digest("hex").slice(0, 24);
	const directory = path.join(stateDir, "logs");
	const file = path.join(directory, "browser-tab-close.jsonl");
	return (failure: BrowserCloseFailure): void => {
		try {
			const code = (failure.error as { code?: unknown } | null)?.code;
			const name = (failure.error as { name?: unknown } | null)?.name;
			const line = JSON.stringify({
				timestamp: new Date().toISOString(), event: "browser_tab_close_failed", appVersion,
				code: typeof code === "string" && ERROR_CODES.has(code) ? code : "UNKNOWN",
				errorType: typeof name === "string" && ERROR_TYPES.has(name) ? name : "UNKNOWN",
				session: opaque(failure.sessionId), tab: opaque(failure.tabId),
				tabCount: failure.tabCount, tabPresent: failure.tabPresent,
				wasActive: failure.wasActive, agentTab: failure.agentTab,
			}) + "\n";
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			chmodSync(directory, 0o700);
			try {
				chmodSync(file, 0o600);
				if (statSync(file).size + Buffer.byteLength(line) > BROWSER_CLOSE_LOG_MAX_BYTES)
					renameSync(file, `${file}.1`);
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
			appendFileSync(file, line, { mode: 0o600 });
			chmodSync(file, 0o600);
		} catch {
			// Diagnostics must never replace the original failure (e.g. disk full).
		}
	};
}
