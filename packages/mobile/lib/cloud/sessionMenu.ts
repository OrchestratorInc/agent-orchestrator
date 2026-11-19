import { conversationMenuItem, type ConversationMenuGroup, type ConversationMenuItem, type ConversationMenuState } from "../chat/conversationMenuModel";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";

/**
 * Archiving an Orchestrator Cloud session, said as the desktop says it
 * (archive.bodyCloud: the control plane keeps its conversation and work so it
 * can be re-provisioned), with what the desktop leaves out: the files attached
 * to its messages are deleted with it, and a restored session comes back
 * without them (the control plane's deleteSessionAttachmentsTx).
 */
export const ARCHIVE_CLOUD_SESSION_CONFIRM = {
	...ARCHIVE_SESSION_CONFIRM,
	message: "You can always restore it from the Archive section later — its conversation and work are kept. Files attached to its messages are deleted.",
} as const;

/** The sheet's Archive subtitle for a cloud session: two lines at most, as the local one's. */
export const ARCHIVE_CLOUD_SESSION_HINT = "Stops the agent. Keeps its work; deletes attached files.";

/** What a cloud session's actions sheet knows about it, as plain values. */
export type CloudSessionMenuState = {
	terminated: boolean;
	pinned: boolean;
	/** A Terminal UI session showing its chat: the agent's terminal is the other view. */
	canShowAgentTerminal: boolean;
	/**
	 * The session can switch interface now: a Chat session to Terminal UI, a
	 * Terminal UI one back to Chat. `switching` while a switch starts or runs.
	 */
	interfaceSwitch?: { to: "chat" | "tui"; switching: boolean };
	/** The open (or newest) pull request, whose review page PR Review opens. */
	pullRequest?: { number: number };
	/** The project's name, when the session's project is known. */
	projectName?: string;
	/** Its artifact files, where the organization has artifacts and there are some. */
	artifactCount?: number;
};

/**
 * A cloud session's "⋯" sheet, in the local chat's sections and words: the
 * sandbox shell and the agent's terminal first, then pinning and PR Review
 * (the pull request's page, as a local worker's), then the session itself:
 * its switch between Chat and Terminal UI, Archive last in red (Restore once
 * it is archived). There is nothing to
 * rename here: as for a local session, the board's row renames it. Its
 * artifacts open from Open, as a local session's do. The cloud has no preview
 * or compaction.
 */
export function cloudSessionMenuGroups(state: CloudSessionMenuState): ConversationMenuGroup[] {
	// The local items' labels and glyphs, so the two sheets read the same.
	const base: ConversationMenuState = {
		canPin: true, canCompact: false, canDelete: true, pinned: state.pinned, openingShell: false, compacting: false, turnInFlight: false, artifactCount: state.artifactCount,
		switchingToTerminal: state.interfaceSwitch?.to === "tui" && state.interfaceSwitch.switching,
		switchingToChat: state.interfaceSwitch?.to === "chat" && state.interfaceSwitch.switching,
	};
	const item = (action: ConversationMenuItem["action"]) => conversationMenuItem(action, base);
	const open: ConversationMenuItem[] = [
		...(state.terminated ? [] : [item("shell")]),
		...(state.canShowAgentTerminal ? [item("agent_terminal")] : []),
		...(state.projectName ? [{ ...item("project"), subtitle: state.projectName }] : []),
		...(state.artifactCount ? [item("artifacts")] : []),
	];
	const worker: ConversationMenuItem[] = [
		item("pin"),
		...(state.pullRequest ? [{ ...item("pull_requests"), subtitle: `Pull request #${state.pullRequest.number}` }] : []),
	];
	// As a local chat's sheet: the switch sits with the session, above Archive.
	const switchItem = state.terminated || !state.interfaceSwitch ? [] : [item(state.interfaceSwitch.to === "tui" ? "switch_tui" : "switch_chat")];
	const session: ConversationMenuItem[] = [...switchItem, state.terminated ? item("restore") : { ...item("delete"), subtitle: ARCHIVE_CLOUD_SESSION_HINT }];
	return [
		...(open.length ? [{ title: "Open" as const, items: open }] : []),
		{ title: "Worker" as const, items: worker },
		{ title: "Session" as const, items: session },
	];
}
