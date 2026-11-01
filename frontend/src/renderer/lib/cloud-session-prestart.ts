// Cloud sessions the new chat page started before its brief was sent, so the
// sandbox is already coming up when the user is done typing. Until a send
// claims one it is not a task yet: the session lists leave it out, and one the
// page did not claim (it closed, or the app quit first) is deleted.

const STORAGE_KEY = "ao.cloudPreparedSessions.v1";

/** A pre-started session not yet claimed by a send, by org. */
export interface PreparedCloudSession {
	orgId: string;
	sessionId: string;
}

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
	write([...read().filter((candidate) => candidate.sessionId !== entry.sessionId), entry]);
}

/** Forgets a pre-started session: a send claimed it, or it was deleted. */
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
	return read().filter((candidate) => candidate.orgId === orgId).map((candidate) => candidate.sessionId);
}
