import { create } from "zustand";
import { parseStoredGroups, splitBeside, trimStoredGroups, ungroupById, withoutTab, type SplitSide, type TabGroup } from "../lib/pane-split";

export const SPLIT_GROUPS_STORAGE_KEY = "ao.splitPaneGroups";
/** A shell's placeholder key until the daemon answers; it never outlives a reload. */
const PENDING_SHELL_KEY_PREFIX = "pending-shell:";

function readStoredSplitGroups(): Record<string, TabGroup[]> {
	try {
		return parseStoredGroups(window.localStorage?.getItem(SPLIT_GROUPS_STORAGE_KEY), (key) => key.startsWith(PENDING_SHELL_KEY_PREFIX));
	} catch {
		return {};
	}
}

function writeStoredSplitGroups(groupsBySession: Record<string, TabGroup[]>): void {
	try {
		const trimmed = trimStoredGroups(groupsBySession);
		if (Object.keys(trimmed).length) window.localStorage?.setItem(SPLIT_GROUPS_STORAGE_KEY, JSON.stringify(trimmed));
		else window.localStorage?.removeItem(SPLIT_GROUPS_STORAGE_KEY);
	} catch {
		// Storage full or unavailable: the layout still works for this run.
	}
}

type SplitPanesState = {
	/** Each session's split groups, keyed by the session's UI key. */
	groupsBySession: Record<string, TabGroup[]>;
	/** Applies an update to one session's groups; the session moves to most recent. */
	updateGroups: (sessionKey: string, update: (current: TabGroup[]) => TabGroup[]) => void;
	/** Shows `key` on `side` of `anchor`, joining the anchor's group or forming one. */
	splitBeside: (sessionKey: string, anchor: string, key: string, side: SplitSide) => void;
	/** Takes one tab out of its group; a group left with one tab dissolves. */
	removeTab: (sessionKey: string, key: string) => void;
	/** Every tab of the group becomes a tab of its own again. */
	ungroup: (sessionKey: string, groupId: string) => void;
};

const EMPTY: TabGroup[] = [];

/**
 * Split layouts, outside any component, so whatever drives a split (a tab
 * drop, a shortcut, or later an agent asking for one) goes through the same
 * actions and anything can read the current layout. SessionView reconciles
 * them against the tabs that exist and renders the panes. Layouts are
 * remembered across reloads.
 */
export const useSplitPanesStore = create<SplitPanesState>((set, get) => ({
	groupsBySession: readStoredSplitGroups(),
	updateGroups: (sessionKey, update) => {
		const current = get().groupsBySession;
		const previous = current[sessionKey] ?? EMPTY;
		const next = update(previous);
		if (next === previous) return;
		const { [sessionKey]: _removed, ...rest } = current;
		const groupsBySession = next.length ? { ...rest, [sessionKey]: next } : rest;
		set({ groupsBySession });
		writeStoredSplitGroups(groupsBySession);
	},
	splitBeside: (sessionKey, anchor, key, side) => get().updateGroups(sessionKey, (groups) => splitBeside(groups, anchor, key, side)),
	removeTab: (sessionKey, key) => get().updateGroups(sessionKey, (groups) => withoutTab(groups, key)),
	ungroup: (sessionKey, groupId) => get().updateGroups(sessionKey, (groups) => ungroupById(groups, groupId)),
}));

export function useSessionSplitGroups(sessionKey: string): TabGroup[] {
	return useSplitPanesStore((state) => state.groupsBySession[sessionKey] ?? EMPTY);
}
