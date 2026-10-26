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
import { symbols, type PlatformSymbol } from "./symbols";

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

/**
 * The row's menu as screen-reader actions, for VoiceOver's Actions rotor and
 * TalkBack's actions menu: neither reader can swipe a row open or long-press
 * it, so without these pin, rename and archive were out of reach. Open is the
 * row's own activation and is not repeated.
 */
export function workerAccessibilityActions(actions: readonly WorkerAction[]): { name: WorkerActionId; label: string }[] {
	return actions.filter((action) => action.id !== "open").map((action) => ({ name: action.id, label: action.title }));
}

/** The action a screen reader asked for, if the row offers it. */
export function workerActionNamed(actions: readonly WorkerAction[], name: string): WorkerActionId | undefined {
	return actions.find((action) => action.id === name)?.id;
}

/** Confirmation copy for every Archive entry point, word for word desktop's (SessionArchiveDialog). */
export const ARCHIVE_SESSION_CONFIRM = {
	title: "Are you sure you want to archive this session?",
	message: "You can always restore it from the Archive section later.",
	cancel: "No",
	confirm: "Confirm",
} as const;

export const ARCHIVE_SESSION_HINT = "Stops the agent and moves it to Archive. Deletes the worktree folder unless it has uncommitted changes.";

/**
 * Each action's glyph as each platform's own symbol: the SF Symbol iOS's menu
 * resolves by name, and the Material Symbol Android's menu draws from a bundled
 * drawable named after it (assets/icons/<android name>.xml).
 *
 * Typed against the symbol registry's types, so a name iOS 16.4 cannot draw or
 * a Material name that does not exist is a compile error rather than a blank
 * icon on the device. The registry is plain data, so this module still runs
 * under Node for its tests.
 */
export const workerActionSymbols = {
	open: { ios: "arrow.forward", android: "arrow_forward" },
	pin: symbols.pin,
	unpin: symbols.unpin,
	rename: symbols.rename,
	resume: symbols.refresh,
	restore: symbols.refresh,
	openPr: { ios: "arrow.up.forward.square", android: "open_in_new" },
	delete: symbols.archive,
} as const satisfies Record<WorkerActionId, PlatformSymbol>;

/** The Material Symbols Android's menu bundles a drawable for, one per action. */
export type WorkerActionDrawable = (typeof workerActionSymbols)[WorkerActionId]["android"];

/**
 * The action's glyph wherever it is drawn: the long-press menu on both
 * platforms and the swipe rail, so one action never reads two ways.
 */
export function workerActionSymbol(id: WorkerActionId): PlatformSymbol {
	return workerActionSymbols[id];
}
