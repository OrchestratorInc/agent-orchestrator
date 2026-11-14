import type { ConversationActivity } from "../../types/conversation";
import { binaryName, commandIntent, READ_PROGRAMS, SEARCH_PROGRAMS } from "./activity-step";

export type CommandCategory = "read" | "search" | "vcs" | "run";

export const ACTIVITY_SUMMARY_BUTTON_CLASS =
	"activity-summary-button group/run flex w-full select-none items-center gap-2 py-0.5 pr-1 text-left outline-none focus-visible:outline-none";

export function isNonzeroCommandExit(activity: ConversationActivity): boolean {
	const exitCode = activity.detail?.exitCode;
	return (
		activity.activityKind === "command" &&
		activity.status === "failed" &&
		typeof exitCode === "number" &&
		exitCode !== 0
	);
}

export function isFailedCall(activity: ConversationActivity): boolean {
	if (isNonzeroCommandExit(activity)) return false;
	if (activity.status === "failed") return true;
	return activity.activityKind === "mcp_tool" && (activity.detail?.success === false || Boolean(activity.detail?.error));
}

const VCS = new Set(["git", "gh"]);

export function commandCategory(text: string): CommandCategory {
	if (/^Searched the web\b/.test(text)) return "search";
	const { program: binary = "", writes } = commandIntent(text);
	if (writes) return "run";
	if (READ_PROGRAMS.has(binary)) return "read";
	if (SEARCH_PROGRAMS.has(binary)) return "search";
	if (VCS.has(binary)) return "vcs";
	return "run";
}

export function commandBinaryLabel(command: string): string | undefined {
	const trimmed = command.trim();
	if (!trimmed) return undefined;
	const word = firstWord(trimmed);
	return word || undefined;
}

function firstWord(text: string): string {
	const trimmed = text.trim();
	const space = trimmed.indexOf(" ");
	const head = space > 0 ? trimmed.slice(0, space) : trimmed;
	return binaryName(head);
}
