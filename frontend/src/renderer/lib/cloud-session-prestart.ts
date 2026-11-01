// Cloud sessions the new chat page started before its brief was sent, so the
// sandbox is already coming up when the user is done typing. Until a send
// claims one it is not a task yet: the session lists leave it out, and one the
// page did not claim (it closed, or the app quit first) is deleted. A deleted
// one is still listed by the control plane (as deleted), so it stays left out.

const STORAGE_KEY = "ao.cloudPreparedSessions.v1";

/** A pre-started session no send claimed, by org; `discarded` once it was deleted. */
export interface PreparedCloudSession {
	orgId: string;
	sessionId: string;
	discarded?: boolean;
}

// Deleted ones are kept only so the lists keep leaving them out; the oldest go first.
const MAX_REMEMBERED = 200;

let prepared: PreparedCloudSession[] | undefined;

function read(): PreparedCloudSession[] {
	if (prepared) return prepared;
	try {
		const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "[]");
		prepared = Array.isArray(saved)
			? saved.filter((entry): entry is PreparedCloudSession =>
				Boolean(entry) && typeof entry.orgId === "string" && typeof entry.sessionId === "string")
			: [];
	} catch {
		prepared = [];
	}
	return prepared;
}

function write(next: PreparedCloudSession[]) {
	prepared = next;
	try {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
	} catch {
		// Kept for this run; an unclaimed session left by a crash then shows in the list.
	}
}

/** Records a session the page started ahead of its brief. */
export function rememberPreparedCloudSession(entry: PreparedCloudSession): void {
	write([...read().filter((candidate) => candidate.sessionId !== entry.sessionId), entry].slice(-MAX_REMEMBERED));
}

/** Records that an unclaimed pre-started session was deleted: no list shows it, and nothing deletes it again. */
export function discardedPreparedCloudSession(sessionId: string): void {
	write(read().map((candidate) => candidate.sessionId === sessionId ? { ...candidate, discarded: true } : candidate));
}

/** Forgets a pre-started session a send claimed: it is a task now. */
export function forgetPreparedCloudSession(sessionId: string): void {
	if (!read().some((candidate) => candidate.sessionId === sessionId)) return;
	write(read().filter((candidate) => candidate.sessionId !== sessionId));
}

/** Whether a listed session is a pre-started one no send has claimed, which no list shows. */
export function isUnclaimedPreparedCloudSession(sessionId: string): boolean {
	return read().some((candidate) => candidate.sessionId === sessionId);
}

/** The unclaimed pre-started sessions of an org, such as ones an earlier run left behind. */
export function preparedCloudSessions(orgId: string): string[] {
	return read().filter((candidate) => candidate.orgId === orgId && !candidate.discarded).map((candidate) => candidate.sessionId);
}
