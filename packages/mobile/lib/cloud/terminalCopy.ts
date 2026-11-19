// What a cloud terminal says about its connection, in the local terminal's
// words (lib/session/terminalCopy.ts). Pure, so the wording is unit-testable.
import type { CloudTerminalStatus } from "./terminal";
import type { CloudTerminalKind } from "./types";

/**
 * How the terminal presents its connection:
 * - `live`: nothing; the grid speaks for itself.
 * - `pending`: a pill floating over the grid, as the local terminal's
 *   "Connecting…" / "Reconnecting…".
 * - `ended`: the process behind the terminal is gone; an empty state covers the
 *   grid, as the local terminal's "Shell closed" / "Session terminated".
 * - `failed`: the transport gave up (it retries on its own until then); an
 *   empty state with a way to try again.
 */
export type CloudTerminalDisplay =
	| { state: "live" }
	| { state: "pending"; label: string }
	| { state: "ended"; title: string; message: string; action?: string }
	| { state: "failed"; title: string; message: string; action: string };

export function cloudTerminalDisplay(status: CloudTerminalStatus, note: string | undefined, kind: CloudTerminalKind): CloudTerminalDisplay {
	switch (status) {
		case "open":
			return { state: "live" };
		// The transport's own notes ("Waiting for the sandbox to start…",
		// "Starting the terminal…", "Reconnecting…") are already pill-sized.
		case "connecting":
			return { state: "pending", label: note || "Connecting…" };
		// Only reported once the view has closed the connection itself, which it
		// does on the way out or before opening the next one.
		case "closed":
			return { state: "pending", label: "Connecting…" };
		case "exited":
			// A new workspace ticket starts a fresh shell; an exited agent stays
			// exited until the session is resumed, which reconnects on its own.
			return kind === "workspace"
				? { state: "ended", title: "Shell closed", message: "This sandbox shell is no longer running.", action: "Open a new shell" }
				: { state: "ended", title: "Agent terminal ended", message: "The agent's terminal has exited. Start a new session to continue." };
		case "error":
			return { state: "failed", title: "Terminal unavailable", message: note || "The connection to the sandbox was lost.", action: "Try again" };
	}
}

/** The dock's line when this ticket may watch the terminal but not type in it. */
export const READ_ONLY_COPY = "You can watch this terminal, but not type in it.";
