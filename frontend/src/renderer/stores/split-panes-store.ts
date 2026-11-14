import { create } from "zustand";
import { parseStoredGroups, splitBeside, trimStoredGroups, ungroupById, withoutTab, type SplitSide, type TabGroup } from "../lib/pane-split";

export const SPLIT_GROUPS_STORAGE_KEY = "ao.splitPaneGroups";
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
	}
}

type SplitPanesState = {
	groupsBySession: Record<string, TabGroup[]>;
	updateGroups: (sessionKey: string, update: (current: TabGroup[]) => TabGroup[]) => void;
	splitBeside: (sessionKey: string, anchor: string, key: string, side: SplitSide) => void;
	removeTab: (sessionKey: string, key: string) => void;
	ungroup: (sessionKey: string, groupId: string) => void;
};

const EMPTY: TabGroup[] = [];

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
