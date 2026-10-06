import { create } from "zustand";

export type SessionGitActionKind = "createPR" | "createDraftPR" | "commit" | "commitPush" | "push";

export type PendingSessionGitAction = {
	kind: SessionGitActionKind;
	startedAt: number;
	/** The session was seen working on the request, so a later idle means it finished. */
	sawActive?: boolean;
	/** When the session went idle without the expected git facts appearing. */
	idleSince?: number;
};

// The inspector's commit/push/PR button stays in its loading state from the
// click until the workspace facts show the result. The Summary view unmounts on
// every tab switch, so the pending action lives here, keyed by sessionUiKey.
//
// Not persisted — a reload re-derives everything from the workspace facts.
type SessionGitActionState = {
	pending: Record<string, PendingSessionGitAction>;
	start: (key: string, kind: SessionGitActionKind) => void;
	update: (key: string, patch: Partial<PendingSessionGitAction>) => void;
	clear: (key: string) => void;
};

export const useSessionGitActionStore = create<SessionGitActionState>((set) => ({
	pending: {},
	start: (key, kind) => set((state) => ({ pending: { ...state.pending, [key]: { kind, startedAt: Date.now() } } })),
	update: (key, patch) =>
		set((state) => {
			const current = state.pending[key];
			return current ? { pending: { ...state.pending, [key]: { ...current, ...patch } } } : state;
		}),
	clear: (key) =>
		set((state) => {
			if (!(key in state.pending)) return state;
			const { [key]: _cleared, ...rest } = state.pending;
			return { pending: rest };
		}),
}));
