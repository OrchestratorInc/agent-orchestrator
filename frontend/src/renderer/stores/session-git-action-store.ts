import { create } from "zustand";

export type SessionGitActionKind = "createPR" | "createDraftPR" | "commit" | "commitPush" | "push" | "updateBase" | "resolveConflicts";

export type PendingSessionGitAction = {
	kind: SessionGitActionKind;
	startedAt: number;
	sawActive?: boolean;
	idleSince?: number;
};

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
