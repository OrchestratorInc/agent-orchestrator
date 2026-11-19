import { useEffect } from "react";
import { create } from "zustand";
import { apiErrorCode } from "./api-client";
import { CloudCpError, type CloudCpClient } from "./cloud-cp";
import { clientForSessionHost } from "./host-clients";
import { useCloudCp } from "../hooks/useCloudCp";

/**
 * The last step of a move, kept until it succeeds. Once the session runs at
 * its destination the move is not undone, so a failed last step is retried:
 * recording on this computer that a session moved to Orchestrator Cloud, or
 * deleting the Cloud session a session left for this computer. Both are safe
 * to repeat.
 */
export type UnfinishedMove =
	| { kind: "complete"; host?: string; sessionId: string; moveId: string; orgId: string; cloudSessionId: string }
	| { kind: "delete-cloud"; orgId: string; cloudSessionId: string };

const STORAGE_KEY = "ao.move.unfinished";
const RETRY_MS = 30_000;

function key(move: UnfinishedMove): string {
	return move.kind === "complete" ? `complete:${move.host ?? ""}:${move.sessionId}` : `delete-cloud:${move.orgId}:${move.cloudSessionId}`;
}

function load(): UnfinishedMove[] {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "[]");
		return Array.isArray(saved) ? (saved as UnfinishedMove[]) : [];
	} catch {
		return [];
	}
}

function save(moves: UnfinishedMove[]) {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(moves));
	} catch {}
}

type UnfinishedMoveStore = {
	moves: UnfinishedMove[];
	add: (move: UnfinishedMove) => void;
	remove: (move: UnfinishedMove) => void;
};

export const useUnfinishedMoveStore = create<UnfinishedMoveStore>((set, get) => ({
	moves: load(),
	add: (move) => {
		const moves = [...get().moves.filter((m) => key(m) !== key(move)), move];
		save(moves);
		set({ moves });
	},
	remove: (move) => {
		const moves = get().moves.filter((m) => key(m) !== key(move));
		save(moves);
		set({ moves });
	},
}));

/** Tries the move's last step once; true when it is done or no longer needed. */
export async function finishMove(
	move: UnfinishedMove,
	deps: { daemon: (host?: string) => Pick<ReturnType<typeof clientForSessionHost>, "POST">; cloud: Pick<CloudCpClient, "deleteSession"> | null },
): Promise<boolean> {
	if (move.kind === "complete") {
		const { error } = await deps.daemon(move.host).POST("/api/v1/sessions/{sessionId}/move/complete", {
			params: { path: { sessionId: move.sessionId } },
			body: { moveId: move.moveId, orgId: move.orgId, sessionId: move.cloudSessionId },
		});
		// A session that is gone, or moved under another move, has nothing left to record.
		return !error || ["SESSION_NOT_FOUND", "SESSION_MOVED"].includes(apiErrorCode(error) ?? "");
	}
	if (!deps.cloud) return false;
	try {
		await deps.cloud.deleteSession(move.orgId, move.cloudSessionId);
		return true;
	} catch (error) {
		return error instanceof CloudCpError && error.status === 404;
	}
}

/** Retries unfinished moves at start and every 30 s until each is done. */
export function useFinishUnfinishedMoves() {
	const { client } = useCloudCp();
	const pending = useUnfinishedMoveStore((state) => state.moves.length);
	useEffect(() => {
		if (pending === 0) return;
		let running = false;
		const run = async () => {
			if (running) return;
			running = true;
			try {
				for (const move of useUnfinishedMoveStore.getState().moves) {
					const done = await finishMove(move, { daemon: clientForSessionHost, cloud: client }).catch(() => false);
					if (done) useUnfinishedMoveStore.getState().remove(move);
				}
			} finally {
				running = false;
			}
		};
		void run();
		const timer = window.setInterval(() => void run(), RETRY_MS);
		return () => window.clearInterval(timer);
	}, [client, pending]);
}
