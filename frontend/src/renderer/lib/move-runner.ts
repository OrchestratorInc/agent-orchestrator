import { create } from "zustand";
import { LOCAL_HOST } from "./hosts";
import { MoveError, moveSession, type MoveInput, type MoveResult, type MoveStep } from "./host-move";

/**
 * asking: picked while Claude was mid-turn, waiting for the user to choose.
 * waiting: queued until the current turn ends. failed: stopped before the destination took it.
 */
export type MovePhase = "asking" | "waiting" | "moving" | "failed" | "done";

export type MoveTarget = { hostId: string; projectId: string; label: string };

export type MoveState = {
	phase: MovePhase;
	targetHostId: string;
	targetProjectId: string;
	targetLabel: string;
	/** The session's title, for the toast shown while its view is closed. */
	title?: string;
	step?: MoveStep;
	error?: MoveError;
	result?: MoveResult;
};

/**
 * Moves in progress, keyed by moveKey, and how many session views show each.
 * A move outlives the menu that started it; a move no view shows gets a toast.
 */
export const useMoveStore = create<{ moves: Record<string, MoveState>; shown: Record<string, number> }>(() => ({ moves: {}, shown: {} }));

export const moveKey = (hostId: string | undefined, sessionId: string) => `${hostId ?? LOCAL_HOST}:${sessionId}`;

/** Records that a session view shows this move; call the result when it stops. */
export function showMove(key: string): () => void {
	const add = (by: number) => useMoveStore.setState((state) => ({ shown: { ...state.shown, [key]: (state.shown[key] ?? 0) + by } }));
	add(1);
	return () => add(-1);
}

export const moveShown = (key: string) => (useMoveStore.getState().shown[key] ?? 0) > 0;

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

/** Holds a move picked mid-turn until the user says when it should run. */
export function askMove(key: string, target: MoveTarget, title?: string) {
	const phase = useMoveStore.getState().moves[key]?.phase;
	if (phase === "waiting" || phase === "moving") return;
	useMoveStore.setState((state) => ({
		moves: { ...state.moves, [key]: { phase: "asking", targetHostId: target.hostId, targetProjectId: target.projectId, targetLabel: target.label, title } },
	}));
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
	title?: string;
	onDone?: (result: MoveResult) => void;
	onFail?: (error: MoveError) => void;
};

/** Runs moveSession for one session, recording its progress in useMoveStore. */
export async function runMove(key: string, input: MoveInput, options: RunMoveOptions = {}): Promise<void> {
	// A second start while one runs, such as a double click, is ignored.
	const phase = useMoveStore.getState().moves[key]?.phase;
	if (phase === "waiting" || phase === "moving") return;
	cancelled.delete(key);
	useMoveStore.setState((state) => ({
		moves: {
			...state.moves,
			[key]: { phase: options.afterTurn ? "waiting" : "moving", targetHostId: input.target.hostId, targetProjectId: input.target.projectId, targetLabel: input.target.label, title: options.title },
		},
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
		update(key, { phase: "moving", step: "prepare", error: undefined });
		try {
			const result = await moveSession({
				...input,
				stopTurn: options.stopTurn,
				onStep: (step) => update(key, { step }),
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
