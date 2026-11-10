import { create } from "zustand";
import { sessionUiKey } from "../lib/hosts";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

export const REMOTE_UNPINS_STORAGE_KEY = "ao.sidebar.remote-unpins";

/**
 * A pin removed on this computer from a session that another host owns. The
 * pin itself lives in that host's daemon, which may be asleep, offline, or no
 * longer linked, so the removal is kept here until the host's own list agrees.
 * `at` is when it was removed, in this computer's epoch milliseconds;
 * `pinnedAt` is the host's time of the pin that was removed.
 */
export type RemoteUnpin = { hostId: string; sessionId: string; at: number; pinnedAt?: string };

type RemoteUnpins = Record<string, RemoteUnpin>;

function readStored(): RemoteUnpins {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(REMOTE_UNPINS_STORAGE_KEY) ?? "null");
		if (!Array.isArray(saved)) return {};
		const valid = saved.filter((entry): entry is RemoteUnpin =>
			typeof entry?.hostId === "string" && typeof entry?.sessionId === "string" && typeof entry?.at === "number");
		return Object.fromEntries(valid.map((entry) => [sessionUiKey(entry.sessionId, entry.hostId), entry]));
	} catch {
		return {};
	}
}

function store(unpins: RemoteUnpins) {
	try {
		const entries = Object.values(unpins);
		if (entries.length > 0) window.localStorage.setItem(REMOTE_UNPINS_STORAGE_KEY, JSON.stringify(entries));
		else window.localStorage.removeItem(REMOTE_UNPINS_STORAGE_KEY);
	} catch {}
}

type RemoteUnpinState = {
	/** Keyed by `sessionUiKey(sessionId, hostId)`. */
	unpins: RemoteUnpins;
	add: (hostId: string, sessionId: string, pinnedAt?: string) => void;
	remove: (keys: readonly string[]) => void;
	/** Drops removals for hosts that are no longer saved: their sessions are gone from this computer. */
	keepHosts: (hostIds: readonly string[]) => void;
};

export const useRemoteUnpinStore = create<RemoteUnpinState>((set, get) => {
	const replace = (unpins: RemoteUnpins) => {
		store(unpins);
		set({ unpins });
	};
	return {
		unpins: readStored(),
		add: (hostId, sessionId, pinnedAt) => {
			replace({ ...get().unpins, [sessionUiKey(sessionId, hostId)]: { hostId, sessionId, at: Date.now(), ...(pinnedAt ? { pinnedAt } : {}) } });
		},
		remove: (keys) => {
			const { unpins } = get();
			if (!keys.some((key) => key in unpins)) return;
			const next = { ...unpins };
			for (const key of keys) delete next[key];
			replace(next);
		},
		keepHosts: (hostIds) => {
			const { unpins } = get();
			const hosts = new Set(hostIds);
			const stale = Object.keys(unpins).filter((key) => !hosts.has(unpins[key].hostId));
			if (stale.length > 0) get().remove(stale);
		},
	};
});

/** Whether the host's pin is the one removed here. A pin set again later, from
 *  the other computer, wins. Both times are the host's, so its clock being off
 *  from this computer's cannot hide a removal. */
export function isUnpinnedHere(unpin: RemoteUnpin | undefined, pinnedAt: string | null | undefined): boolean {
	if (!unpin) return false;
	if (!pinnedAt || !unpin.pinnedAt) return true;
	const pinned = Date.parse(pinnedAt);
	const removed = Date.parse(unpin.pinnedAt);
	return !(Number.isFinite(pinned) && Number.isFinite(removed) && pinned > removed);
}

/** A remote host's workspace as this computer shows it: sessions unpinned here read as unpinned. */
export function withRemoteUnpins(workspace: WorkspaceSummary, unpins: RemoteUnpins): WorkspaceSummary {
	const unpinned = (session: WorkspaceSession) =>
		session.isPinned === true && isUnpinnedHere(unpins[sessionUiKey(session.id, session.hostId)], session.pinnedAt);
	if (!workspace.sessions.some(unpinned)) return workspace;
	return {
		...workspace,
		sessions: workspace.sessions.map((session) => unpinned(session) ? { ...session, isPinned: false, pinnedAt: undefined } : session),
	};
}
