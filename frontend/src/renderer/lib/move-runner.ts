import { create } from "zustand";
import { LOCAL_HOST } from "./hosts";
import { MoveError, moveSession, type MoveInput, type MoveResult, type MoveStep } from "./move-session";

/** waiting: queued until the current turn ends. failed: stopped before the destination took it. */
export type MovePhase = "waiting" | "moving" | "failed" | "done";

export type MoveState = {
	phase: MovePhase;
	targetHostId: string;
	targetLabel: string;
	step?: MoveStep;
	progress?: { sent: number; total: number };
	error?: MoveError;
	result?: MoveResult;
};

/**
 * Moves in progress, keyed by moveKey. They outlive the dialog that started
 * them, so the session views and a later dialog can show where they are.
 */
export const useMoveStore = create<{ moves: Record<string, MoveState> }>(() => ({ moves: {} }));

export const moveKey = (hostId: string | undefined, sessionId: string) => `${hostId ?? LOCAL_HOST}:${sessionId}`;

const cancelled = new Set<string>();
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

function update(key: string, next: Partial<MoveState>) {
	useMoveStore.setState((state) => ({ moves: { ...state.moves, [key]: { ...state.moves[key], ...next } as MoveState } }));
}

export function clearMove(key: string) {
	useMoveStore.setState((state) => {
		const { [key]: _, ...rest } = state.moves;
		return { moves: rest };
	});
}

/** Stops a move that is still waiting for its turn to end. */
export function cancelWaitingMove(key: string) {
	if (useMoveStore.getState().moves[key]?.phase !== "waiting") return;
	cancelled.add(key);
	clearMove(key);
}

async function turnRunning(source: MoveInput["source"]): Promise<boolean> {
	const res = await source.client.GET("/api/v1/sessions/{sessionId}", { params: { path: { sessionId: source.sessionId } } });
	const state = res.data?.session.activity?.state;
	return state === "active" || state === "blocked";
}

export type RunMoveOptions = {
	/** Wait for the current turn to end, then move. */
	afterTurn?: boolean;
	/** End the current turn and move now. */
	stopTurn?: boolean;
	onDone?: (result: MoveResult) => void;
	onFail?: (error: MoveError) => void;
};

/** Runs moveSession for one session, recording its progress in useMoveStore. */
export async function runMove(key: string, input: MoveInput, options: RunMoveOptions = {}): Promise<void> {
	cancelled.delete(key);
	useMoveStore.setState((state) => ({
		moves: { ...state.moves, [key]: { phase: options.afterTurn ? "waiting" : "moving", targetHostId: input.target.hostId, targetLabel: input.target.label } },
	}));
	for (;;) {
		if (options.afterTurn) {
			update(key, { phase: "waiting" });
			while (await turnRunning(input.source).catch(() => true)) {
				await sleep(2000);
				if (cancelled.delete(key)) return;
			}
			if (cancelled.delete(key)) return;
		}
		update(key, { phase: "moving", step: "prepare", progress: undefined, error: undefined });
		try {
			const result = await moveSession({
				...input,
				stopTurn: options.stopTurn,
				onStep: (step) => update(key, { step }),
				onProgress: (sent, total) => update(key, { progress: { sent, total } }),
			});
			update(key, { phase: "done", result });
			options.onDone?.(result);
			return;
		} catch (error) {
			const moveError = error instanceof MoveError ? error : new MoveError(String(error), "prepare", false);
			// The user started another turn while this one waited: wait for that too.
			if (options.afterTurn && moveError.code === "TRANSFER_AGENT_BUSY") continue;
			update(key, { phase: "failed", error: moveError });
			options.onFail?.(moveError);
			return;
		}
	}
}
