import type { FeatherIconName } from "./icons";
/**
 * What the worker row's long-press menu offers, given one session's facts.
 *
 * Pure — no React Native imports — so the rules are unit-testable, the same
 * split the codebase uses for agentsView.ts and orchestratorView.ts.
 *
 * Every action here maps to an endpoint that actually exists. That constraint
 * is the whole design: the obvious missing item is "Stop", and there is no stop
 * or pause endpoint. The archive action calls `killSession`: it stops the agent
 * and deletes the session's worktree folder unless that folder has uncommitted
 * changes (the daemon refuses to remove a dirty worktree). The conversation and
 * the branch's commits stay, which is what Restore rebuilds from. A "Stop" entry
 * would therefore be a second name for the same call.
 */
import type { SFSymbol } from "sf-symbols-typescript";

export type WorkerActionId = "open" | "pin" | "unpin" | "rename" | "resume" | "restore" | "openPr" | "delete";

export type WorkerAction = {
	id: WorkerActionId;
	title: string;
	/** Marks an irreversible row action as destructive in the native menu. */
	destructive?: boolean;
};

export type WorkerActionState = {
	pinned: boolean;
	/** The AO session itself is terminated — only a restore brings it back. */
	terminated: boolean;
	/** The runtime is stopped but the session is alive; the agent can be resumed. */
	stopped: boolean;
	hasPr: boolean;
};

/**
 * Resume and Restore are deliberately exclusive, and mirror how the chat screen
 * already chooses between them: a terminated AO session is restored, a merely
 * stopped agent is resumed. Offering both at once would ask the user to know a
 * distinction the app is supposed to make for them.
 */
export function workerContextActions(state: WorkerActionState): WorkerAction[] {
	const actions: WorkerAction[] = [{ id: "open", title: "Open" }];

	if (state.terminated) actions.push({ id: "restore", title: "Restore session" });
	else if (state.stopped) actions.push({ id: "resume", title: "Resume agent" });

	actions.push(state.pinned ? { id: "unpin", title: "Unpin" } : { id: "pin", title: "Pin" });
	actions.push({ id: "rename", title: "Rename" });

	if (state.hasPr) actions.push({ id: "openPr", title: "Review pull request" });

	// The internal id predates Archive and stays for action routing. Still
	// destructive: archiving deletes a clean worktree folder.
	actions.push({ id: "delete", title: "Archive session", destructive: true });
	return actions;
}

/** Confirmation copy for every Archive entry point; see the module comment. */
export function archiveSessionMessage(name: string): string {
	return `This stops ${name} and moves it to Archive. Its worktree folder is deleted unless it has uncommitted changes. The conversation and committed work on its branch are kept, so you can restore it later.`;
}

export const ARCHIVE_SESSION_HINT = "Stops the agent and moves it to Archive. Deletes the worktree folder unless it has uncommitted changes.";

/**
 * SF Symbol per action, for iOS's native menu.
 *
 * Typed as SFSymbol rather than string so a name that does not exist is a
 * compile error instead of a blank icon on the device — MenuAction.image takes
 * `SFSymbol | ImageSourcePropType`, and a plain string satisfies neither. Same
 * approach as notification-type-icon.ios.tsx. The import is type-only, so this
 * module still runs under Node for its tests.
 */
export function workerActionSymbol(id: WorkerActionId): SFSymbol {
	switch (id) {
		case "open":
			return "arrow.forward";
		case "restore":
		case "resume":
			return "arrow.clockwise";
		case "pin":
			return "pin";
		case "unpin":
			return "pin.slash";
		case "rename":
			return "pencil";
		case "openPr":
			return "arrow.up.forward.square";
		default:
			return "archivebox";
	}
}

/**
 * Which actions Android and the web fallback can show an icon for.
 *
 * Those menus take a bundled drawable via `require`, and a missing file fails at
 * bundle time rather than degrading — so this list has to track assets/icons
 * exactly. Every action now has one, which matches iOS, where the menu resolves
 * an SF Symbol by name for each.
 */
export const WORKER_ACTION_DRAWABLES: readonly WorkerActionId[] = ["pin", "unpin", "rename", "open", "resume", "restore", "openPr", "delete"];

/**
 * Icon for each action, for Android's own action sheet.
 *
 * Android does not render the native MenuView — its rows are an in-app Modal —
 * so it cannot use the bundled drawables above. A vector font needs no asset
 * pipeline at all, and gives that list the same iconography iOS gets from SF
 * Symbols.
 *
 * Pin and unpin carry the same pushpin the swipe rail uses, which comes from a
 * different icon set — hence the family tag. A menu and a rail offering the same
 * action should not draw it two different ways, and Feather has only a map
 * marker, which reads as a location rather than a pin. Kept as plain data so
 * this module stays free of React Native.
 */
export type WorkerActionGlyph =
	| { family: "feather"; name: FeatherIconName }


export function workerActionGlyph(id: WorkerActionId): WorkerActionGlyph {
	switch (id) {
		case "open": return { family: "feather", name: "message-square" };
		case "pin": return { family: "feather", name: "pin" };
		case "unpin": return { family: "feather", name: "pin-off" };
		case "rename": return { family: "feather", name: "edit-2" };
		case "resume": return { family: "feather", name: "play" };
		case "restore": return { family: "feather", name: "rotate-ccw" };
		case "openPr": return { family: "feather", name: "git-pull-request" };
		case "delete": return { family: "feather", name: "archive" };
	}
}

export function hasWorkerActionDrawable(id: WorkerActionId): boolean {
	return WORKER_ACTION_DRAWABLES.includes(id);
}
