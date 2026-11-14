import { create } from "zustand";

type TerminalResetState = {
	nonces: Record<string, number>;
	reconnecting: Record<string, boolean>;
	baselineEpoch: Record<string, number>;
	bump: (sessionId: string, currentEpoch: number) => void;
	advanceEpoch: (sessionId: string) => void;
	markConnected: (sessionId: string) => void;
};

export const useTerminalResetStore = create<TerminalResetState>((set) => ({
	nonces: {},
	reconnecting: {},
	baselineEpoch: {},
	bump: (sessionId, currentEpoch) =>
		set((state) => ({
			nonces: { ...state.nonces, [sessionId]: (state.nonces[sessionId] ?? 0) + 1 },
			reconnecting: { ...state.reconnecting, [sessionId]: true },
			baselineEpoch: { ...state.baselineEpoch, [sessionId]: currentEpoch },
		})),
	advanceEpoch: (sessionId) =>
		set((state) => ({
			nonces: { ...state.nonces, [sessionId]: (state.nonces[sessionId] ?? 0) + 1 },
		})),
	markConnected: (sessionId) =>
		set((state) =>
			state.reconnecting[sessionId]
				? { reconnecting: { ...state.reconnecting, [sessionId]: false } }
				: state,
		),
}));

export function terminalResetNonce(sessionId?: string): number {
	if (!sessionId) return 0;
	return useTerminalResetStore.getState().nonces[sessionId] ?? 0;
}
