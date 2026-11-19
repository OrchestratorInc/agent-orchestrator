import { SIDE_CLOSE_PROTOCOL_VERSION, type SideCloseEntry, type SideCloseState } from "../../../shared/side-close";

export interface SideCloseAttempt {
	requestId: string;
	expectedVersion: number;
	busy: boolean;
}
export interface SideCloseProjection {
	sessionId: string;
	epoch?: string;
	revision: number;
	hydrated: boolean;
	recovery: SideCloseState["recovery"];
	entries: Record<string, SideCloseEntry>;
	attempts: Record<string, SideCloseAttempt>;
}

export function emptyCloseProjection(sessionId: string): SideCloseProjection {
	return { sessionId, revision: -1, hydrated: false, recovery: "unavailable", entries: {}, attempts: {} };
}

export function closeStatus(state: SideCloseProjection, id: string): SideCloseEntry["status"] | undefined {
	if (state.entries[id]?.status === "closed") return "closed";
	const attempt = state.attempts[id];
	if (attempt) return attempt.busy ? "closing" : "unconfirmed";
	return state.entries[id]?.status;
}

export function canMutateSide(state: SideCloseProjection, id: string): boolean {
	const status = closeStatus(state, id);
	return state.hydrated && ((state.recovery === "ready" && status === undefined) || (state.recovery !== "unavailable" && status === "rejected_open"));
}

export function beginClose(state: SideCloseProjection, id: string, requestId: string): SideCloseProjection {
	if (state.entries[id]?.status === "closed") return state;
	return { ...state, attempts: { ...state.attempts, [id]: { requestId, expectedVersion: state.entries[id]?.version ?? 0, busy: true } } };
}

export function uncertainClose(state: SideCloseProjection, id: string, requestId: string): SideCloseProjection {
	const attempt = state.attempts[id];
	if (attempt?.requestId !== requestId) return state;
	return { ...state, attempts: { ...state.attempts, [id]: { ...attempt, busy: false } } };
}

export function unavailableCloseProjection(state: SideCloseProjection, newAttachment = false): SideCloseProjection {
	if (state.hydrated && !newAttachment) return state;
	return {
		...state, hydrated: false, recovery: "unavailable", epoch: newAttachment ? undefined : state.epoch,
		entries: newAttachment ? Object.fromEntries(Object.entries(state.entries).map(([id, entry]) => [id, entry.status === "rejected_open" ? { ...entry, status: "unconfirmed" as const } : entry])) : state.entries,
		attempts: Object.fromEntries(Object.entries(state.attempts).map(([id, attempt]) => [id, { ...attempt, busy: false }])),
	};
}

export function applyCloseSummary(state: SideCloseProjection, summary: SideCloseState, adoptEpoch = false): SideCloseProjection {
	if (!summary || summary.protocolVersion !== SIDE_CLOSE_PROTOCOL_VERSION || !summary.epoch || !Number.isSafeInteger(summary.revision) || summary.revision < 0 ||
		!["ready", "restoring", "unavailable"].includes(summary.recovery) || !Array.isArray(summary.sides) ||
		summary.sides.some(entry => !entry || typeof entry.id !== "string" || typeof entry.sessionId !== "string" || !Number.isSafeInteger(entry.version) || entry.version < 0 || !["closing", "rejected_open", "unconfirmed", "cleanup_pending", "closed"].includes(entry.status)) ||
		(summary.sessionId !== null && summary.sessionId !== state.sessionId) ||
		(!adoptEpoch && summary.epoch !== state.epoch)) return state;
	const sameEpoch = summary.epoch === state.epoch;
	if (sameEpoch && summary.revision < state.revision) return state;
	if (sameEpoch && summary.revision === state.revision && state.hydrated && summary.recovery === state.recovery) return state;
	const entries = { ...state.entries };
	const attempts = { ...state.attempts };
	for (const entry of summary.sides) {
		if (entry.sessionId !== state.sessionId) continue;
		const previous = entries[entry.id];
		// Confirmed shutdown is terminal for an identity, including after recovery.
		if (previous?.status === "closed" && entry.status !== "closed") continue;
		if (previous && entry.version < previous.version) continue;
		if (previous && entry.version === previous.version && (entry.status !== previous.status || entry.operationId !== previous.operationId)) continue;
		entries[entry.id] = entry;
		const local = attempts[entry.id];
		if (local && entry.status !== "closing" && (entry.operationId === local.requestId || entry.version > local.expectedVersion)) delete attempts[entry.id];
	}
	// Missing entries do not revoke a local attempt or previously known close.
	return { ...state, epoch: summary.epoch, revision: summary.revision, hydrated: true, recovery: summary.recovery, entries, attempts };
}

/** Used for every row transition, not only successful HTTP list reads. */
export function selectedSideAfterUpdate<T extends { id: string }>(selected: string | undefined, previous: T[], next: T[]): string | undefined {
	if (selected && next.some(side => side.id === selected)) return selected;
	if (!next.length) return undefined;
	const index = previous.findIndex(side => side.id === selected);
	return index < 0 ? next.at(-1)!.id : next[Math.min(index, next.length - 1)].id;
}

export function withWatchdog<T>(promise: Promise<T>, milliseconds: number): Promise<T> {
	return new Promise<T>((resolve, reject) => {
		const timer = setTimeout(() => reject(new Error("Side chat status timed out.")), milliseconds);
		promise.then(value => { clearTimeout(timer); resolve(value); }, error => { clearTimeout(timer); reject(error); });
	});
}
