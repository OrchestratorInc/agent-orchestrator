import type { ApprovalMode } from "./chat/types";
import type { IOSSymbol } from "./symbols";

/**
 * Each approval mode's symbol, from asking first to asking for nothing, shared
 * by the new-task sheet and the chat composer so a mode reads the same in both.
 * Codex's default is full access (see the chat's `approvalLabel`), so it takes
 * the symbol of never asking rather than of asking first.
 */
export function approvalSymbol(mode: ApprovalMode | string | undefined, harness?: string): IOSSymbol {
	switch (mode) {
		case "default":
			return harness === "codex" ? "exclamationmark.shield" : "hand.raised";
		case "accept-edits":
			return "pencil";
		case "bypass-permissions":
			return "exclamationmark.shield";
		case "auto":
			return "checkmark.shield";
		default:
			// An agent's own permission tier the daemon maps to no mode.
			return "shield";
	}
}
