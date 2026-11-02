import { create } from "zustand";

const STORAGE_KEY = "ao.sidebar.session-read";

type Stored = { baseline: number; readAt: Record<string, number> };

function load(): Stored {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null");
		if (saved && typeof saved === "object") {
			const { baseline, readAt } = saved as Partial<Stored>;
			if (typeof baseline === "number" && readAt && typeof readAt === "object") return { baseline, readAt };
		}
	} catch {}
	// First run: everything that already exists counts as read, so the sidebar
	// does not open with every row marked unread.
	return { baseline: Date.now(), readAt: {} };
}

function persist(state: Stored) {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
	} catch {}
}

export const sessionReadKey = (session: { id: string; hostId?: string }) => `${session.hostId ?? ""}:${session.id}`;

type SessionReadState = Stored & {
	/** The user has the session open: everything up to `at` is read. */
	markRead: (key: string, at: number) => void;
};

/** Which sessions the user has opened since they last changed. Survives restarts. */
export const useSessionReadStore = create<SessionReadState>((set, get) => {
	const initial = load();
	persist(initial);
	return {
		...initial,
		markRead: (key, at) => {
			const current = get();
			if ((current.readAt[key] ?? 0) >= at) return;
			const next = { baseline: current.baseline, readAt: { ...current.readAt, [key]: at } };
			persist(next);
			set(next);
		},
	};
});

/** True when the session changed after the user last had it open. */
export function useSessionUnread(session: { id: string; hostId?: string; updatedAt: string }): boolean {
	const key = sessionReadKey(session);
	const readUntil = useSessionReadStore((state) => state.readAt[key] ?? state.baseline);
	const updated = Date.parse(session.updatedAt);
	return Number.isFinite(updated) && updated > readUntil;
}
