import type { ConversationActivity } from "../../types/conversation";
import { binaryName, commandIntent, READ_PROGRAMS, SEARCH_PROGRAMS } from "./activity-step";

/** The user-facing category of a shell command in the chat timeline. */
export type CommandCategory = "read" | "search" | "vcs" | "run";

/** Shared chrome for one command summary and a collapsed run of commands. */
export const ACTIVITY_SUMMARY_BUTTON_CLASS =
	"activity-summary-button group/run flex w-full select-none items-center gap-1.5 py-0.5 pr-1 text-left outline-none focus-visible:outline-none";

/** A completed shell command that reported an ordinary non-zero process exit. */
export function isNonzeroCommandExit(activity: ConversationActivity): boolean {
	const exitCode = activity.detail?.exitCode;
	return (
		activity.activityKind === "command" &&
		activity.status === "failed" &&
		typeof exitCode === "number" &&
		exitCode !== 0
	);
}

/**
 * A call that really failed, as opposed to a command that exited non-zero (a grep
 * with no match is not an error). These stay visible when the steps around them fold.
 */
export function isFailedCall(activity: ConversationActivity): boolean {
	if (isNonzeroCommandExit(activity)) return false;
	if (activity.status === "failed") return true;
	return activity.activityKind === "mcp_tool" && (activity.detail?.success === false || Boolean(activity.detail?.error));
}

const VCS = new Set(["git", "gh"]);

/**
 * Classify mechanics by intent so the timeline says what happened, not which binary
 * ran. The program is the one the row's own label is named by, past `cd dir &&`,
 * env assignments and wrappers, so the row and its group's header agree: `cat > a`
 * is named as a write, so it does not count as a read.
 */
export function commandCategory(text: string): CommandCategory {
	// Codex reports a web search as a command with only this summary.
	if (/^Searched the web\b/.test(text)) return "search";
	const { program: binary = "", writes } = commandIntent(text);
	if (writes) return "run";
	if (READ_PROGRAMS.has(binary)) return "read";
	if (SEARCH_PROGRAMS.has(binary)) return "search";
	if (VCS.has(binary)) return "vcs";
	return "run";
}

/** The command's binary, normalized so `/bin/sed` and `sed` classify alike. */
export function commandBinaryLabel(command: string): string | undefined {
	const trimmed = command.trim();
	if (!trimmed) return undefined;
	const word = firstWord(trimmed);
	return word || undefined;
}

/** The command's binary, normalized so `/bin/sed` and `sed` classify alike. */
function firstWord(text: string): string {
	const trimmed = text.trim();
	const space = trimmed.indexOf(" ");
	const head = space > 0 ? trimmed.slice(0, space) : trimmed;
	return binaryName(head);
}
