import { useEffect } from "react";
import { create } from "zustand";
import { appI18n } from "../i18n";
import { useUiStore } from "../stores/ui-store";
import { apiErrorCode, apiErrorMessage } from "./api-client";
import { CloudCpError, type CloudCpClient } from "./cloud-cp";
import { clientForSessionHost } from "./host-clients";
import type { CloudSessionRequest, MovePushed } from "./move-session";
import { useCloudCp } from "../hooks/useCloudCp";

/**
 * The last step of a move, kept until it succeeds. Once the session runs at
 * its destination the move is not undone, so a failed last step is retried:
 * recording on this computer that a session moved to Orchestrator Cloud, or
 * deleting the Cloud session a session left for this computer. Both are safe
 * to repeat. addedAt and attempts bound the retries.
 */
export type UnfinishedMove = (
	| { kind: "complete"; host?: string; sessionId: string; moveId: string; orgId: string; cloudSessionId: string }
	| {
			kind: "delete-cloud";
			orgId: string;
			cloudSessionId: string;
			/** The move's export id: the delete holds only while the Cloud session was not used again since. */
			exportMoveId?: string;
	  }
) & {
	addedAt?: number;
	attempts?: number;
	/**
	 * The Orchestrator Cloud account that made the move. A Cloud step runs only
	 * under it: another account would be refused, or told the session does not
	 * exist, which must not read as deleted.
	 */
	accountId?: string;
};

const STORAGE_KEY = "ao.move.unfinished";
const RETRY_MS = 30_000;
/** Retries stop after a day, or after this many tries, whichever comes first. */
export const MAX_FINISH_AGE_MS = 24 * 60 * 60 * 1000;
export const MAX_FINISH_ATTEMPTS = 200;

function key(move: UnfinishedMove): string {
	return move.kind === "complete" ? `complete:${move.host ?? ""}:${move.sessionId}` : `delete-cloud:${move.orgId}:${move.cloudSessionId}`;
}

function load<T>(storageKey: string, fallback: T): T {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? "null");
		return saved && typeof saved === typeof fallback && Array.isArray(saved) === Array.isArray(fallback) ? (saved as T) : fallback;
	} catch {
		return fallback;
	}
}

function save(storageKey: string, value: unknown) {
	try {
		window.localStorage.setItem(storageKey, JSON.stringify(value));
	} catch {}
}

type UnfinishedMoveStore = {
	moves: UnfinishedMove[];
	add: (move: UnfinishedMove) => void;
	remove: (move: UnfinishedMove) => void;
	/** Counts a failed try of a move still in the queue. */
	tried: (move: UnfinishedMove) => void;
};

export const useUnfinishedMoveStore = create<UnfinishedMoveStore>((set, get) => ({
	moves: load<UnfinishedMove[]>(STORAGE_KEY, []),
	add: (move) => {
		const moves = [...get().moves.filter((m) => key(m) !== key(move)), { ...move, addedAt: move.addedAt ?? Date.now(), attempts: move.attempts ?? 0 }];
		save(STORAGE_KEY, moves);
		set({ moves });
	},
	remove: (move) => {
		const moves = get().moves.filter((m) => key(m) !== key(move));
		save(STORAGE_KEY, moves);
		set({ moves });
	},
	tried: (move) => {
		const moves = get().moves.map((m) => (key(m) === key(move) ? { ...m, attempts: (m.attempts ?? 0) + 1 } : m));
		save(STORAGE_KEY, moves);
		set({ moves });
	},
}));

/**
 * One try of a move's last step: "done" when it is done or no longer needed,
 * "retry" when it may work later, or a permanent failure (a refusal such as
 * 403, 409 or 422) with its reason.
 */
export type FinishOutcome = "done" | "retry" | "other-account" | "kept" | { failed: string };

/** Refusals that retrying will not change. */
const PERMANENT_STATUSES = new Set([400, 401, 403, 409, 422]);
/** Refusals with a permanent status that say a retry is safe and may work. */
const RETRYABLE_CODES = new Set(["MOVE_TRANSCRIPT_NOT_CAPTURED"]);

function permanent(status: number | undefined, code: string | undefined): boolean {
	return status !== undefined && PERMANENT_STATUSES.has(status) && !RETRYABLE_CODES.has(code ?? "");
}

/** Tries the move's last step once. */
export async function finishMove(
	move: UnfinishedMove,
	deps: {
		daemon: (host?: string) => Pick<ReturnType<typeof clientForSessionHost>, "POST">;
		cloud: Pick<CloudCpClient, "deleteSession"> | null;
		/** The signed-in Orchestrator Cloud account, "" when signed out. */
		accountId?: string;
	},
): Promise<FinishOutcome> {
	if (move.kind === "complete") {
		const { error, response } = await deps.daemon(move.host).POST("/api/v1/sessions/{sessionId}/move/complete", {
			params: { path: { sessionId: move.sessionId } },
			body: { moveId: move.moveId, orgId: move.orgId, sessionId: move.cloudSessionId },
		});
		if (!error) return "done";
		// A session that is gone, or moved under another move, has nothing left to record.
		if (["SESSION_NOT_FOUND", "SESSION_MOVED"].includes(apiErrorCode(error) ?? "")) return "done";
		const status = (response as { status?: number } | undefined)?.status;
		return permanent(status, apiErrorCode(error)) ? { failed: apiErrorMessage(error, "Could not mark the session as moved") } : "retry";
	}
	if (move.accountId && move.accountId !== deps.accountId) return "other-account";
	if (!deps.cloud) return "retry";
	try {
		await (move.exportMoveId
			? deps.cloud.deleteSession(move.orgId, move.cloudSessionId, { moveId: move.exportMoveId })
			: deps.cloud.deleteSession(move.orgId, move.cloudSessionId));
		return "done";
	} catch (error) {
		if (error instanceof CloudCpError && error.status === 404) return "done";
		// Restarted or used after the move: it is the user's again, not a leftover copy.
		if (error instanceof CloudCpError && error.code === "MOVE_COPY_IN_USE") return "kept";
		if (error instanceof CloudCpError && permanent(error.status, error.code)) return { failed: error.message };
		return "retry";
	}
}

/** Whether a move has been retried long enough to stop. */
export function finishExpired(move: UnfinishedMove, now = Date.now()): boolean {
	return (move.attempts ?? 0) >= MAX_FINISH_ATTEMPTS || now - (move.addedAt ?? now) >= MAX_FINISH_AGE_MS;
}

/** Says once that Orchestrator stopped finishing a move, and what to do instead. */
function reportGaveUp(move: UnfinishedMove, reason?: string) {
	const title = move.kind === "complete" ? appI18n.t("move.finishGaveUp") : appI18n.t("moveHere.finishGaveUp");
	useUiStore.getState().showGlobalToast(title, reason, { tone: "error" });
}

/**
 * Retries unfinished moves at start and every 30 s until each is done, a day
 * has passed, or the step is refused for good; the last two are said once.
 */
export function useFinishUnfinishedMoves() {
	const { client, userId } = useCloudCp();
	const pending = useUnfinishedMoveStore((state) => state.moves.length);
	useEffect(() => {
		if (pending === 0) return;
		let running = false;
		// Said once per move while another account, or none, is signed in.
		const toldOtherAccount = new Set<string>();
		const run = async () => {
			if (running) return;
			running = true;
			try {
				const store = useUnfinishedMoveStore.getState;
				for (const move of store().moves) {
					const outcome = await finishMove(move, { daemon: clientForSessionHost, cloud: client, accountId: client ? userId : "" }).catch((): FinishOutcome => "retry");
					if (outcome === "other-account") {
						// Waits for its account without counting tries, but not past a day.
						if (finishExpired({ ...move, attempts: 0 })) {
							store().remove(move);
							reportGaveUp(move);
						} else if (!toldOtherAccount.has(key(move))) {
							toldOtherAccount.add(key(move));
							useUiStore.getState().showGlobalToast(appI18n.t("moveHere.finishOtherAccount"), undefined, { tone: "info" });
						}
					} else if (outcome === "kept") {
						store().remove(move);
						useUiStore.getState().showGlobalToast(appI18n.t("moveHere.finishKeptInUse"), undefined, { tone: "info" });
					} else if (outcome === "done") {
						store().remove(move);
					} else if (outcome !== "retry") {
						store().remove(move);
						reportGaveUp(move, outcome.failed);
					} else if (finishExpired({ ...move, attempts: (move.attempts ?? 0) + 1 })) {
						store().remove(move);
						reportGaveUp(move);
					} else {
						store().tried(move);
					}
				}
			} finally {
				running = false;
			}
		};
		void run();
		const timer = window.setInterval(() => void run(), RETRY_MS);
		return () => window.clearInterval(timer);
	}, [client, pending, userId]);
}

/**
 * A move to Orchestrator Cloud whose create got no clear answer, kept past
 * the dialog so Try again reuses its id, and so its idempotency key and
 * request: Cloud then returns the session it may have made instead of
 * starting a second one.
 */
export interface UnsettledMove {
	moveId: string;
	orgId: string;
	request: CloudSessionRequest;
	/** The createSession idempotency key; the move id when absent. */
	key?: string;
	pushed?: MovePushed;
	/** A Cloud project this move created. */
	projectId?: string;
	/** The Orchestrator Cloud account that made the move. */
	accountId?: string;
}

const UNSETTLED_KEY = "ao.move.unsettled";

/** The key a session's unsettled move is kept under. */
export function unsettledMoveKey(host: string | undefined, sessionId: string): string {
	return `${host ?? ""}:${sessionId}`;
}

type UnsettledMoveStore = {
	moves: Record<string, UnsettledMove>;
	set: (key: string, move: UnsettledMove | undefined) => void;
};

export const useUnsettledMoveStore = create<UnsettledMoveStore>((set, get) => ({
	moves: load<Record<string, UnsettledMove>>(UNSETTLED_KEY, {}),
	set: (key, move) => {
		const moves = { ...get().moves };
		if (move) moves[key] = move;
		else delete moves[key];
		save(UNSETTLED_KEY, moves);
		set({ moves });
	},
}));

/**
 * A move to this computer whose import may have started a session here, kept
 * past the dialog so a relaunch finishes it under the same move id: this
 * computer then returns that session instead of starting a second one.
 */
export interface UnsettledMoveHere {
	moveId: string;
	/** The project the import ran in; set once it may have started a session here. */
	projectId?: string;
	/** An export Orchestrator Cloud is still running for this move (a 202), which Try again waits on. */
	exportMoveId?: string;
}

const UNSETTLED_HERE_KEY = "ao.move.here";

/** The key a Cloud session's unsettled move here is kept under. */
export function unsettledMoveHereKey(orgId: string, cloudSessionId: string): string {
	return `${orgId}:${cloudSessionId}`;
}

type UnsettledMoveHereStore = {
	moves: Record<string, UnsettledMoveHere>;
	set: (key: string, move: UnsettledMoveHere | undefined) => void;
};

export const useUnsettledMoveHereStore = create<UnsettledMoveHereStore>((set, get) => ({
	moves: load<Record<string, UnsettledMoveHere>>(UNSETTLED_HERE_KEY, {}),
	set: (key, move) => {
		const moves = { ...get().moves };
		if (move) moves[key] = move;
		else delete moves[key];
		save(UNSETTLED_HERE_KEY, moves);
		set({ moves });
	},
}));

/**
 * Where a Cloud session that moved to this computer continues here: the
 * session its move started. The Cloud copy then reads as moved away, and its
 * card links here. Kept on this computer only, newest last, at most
 * MAX_MOVED_HERE entries.
 */
export interface MovedHere {
	projectId: string;
	sessionId: string;
	at: number;
}

const MOVED_HERE_KEY = "ao.move.here.done";
const MAX_MOVED_HERE = 200;

type MovedHereStore = {
	moves: Record<string, MovedHere>;
	record: (orgId: string, cloudSessionId: string, here: Omit<MovedHere, "at">) => void;
};

export const useMovedHereStore = create<MovedHereStore>((set, get) => ({
	moves: load<Record<string, MovedHere>>(MOVED_HERE_KEY, {}),
	record: (orgId, cloudSessionId, here) => {
		const moves = { ...get().moves, [unsettledMoveHereKey(orgId, cloudSessionId)]: { ...here, at: Date.now() } };
		const keys = Object.keys(moves).sort((a, b) => moves[a].at - moves[b].at);
		for (const old of keys.slice(0, Math.max(0, keys.length - MAX_MOVED_HERE))) delete moves[old];
		save(MOVED_HERE_KEY, moves);
		set({ moves });
	},
}));
