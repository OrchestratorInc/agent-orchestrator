/**
 * Sessions listed as shared with the signed-in person during this run of the
 * app, per control plane and account. A shared session's route names no
 * organization, so once its sharing ends and it drops off the shared list the
 * session view could not tell it from one that was cleaned up; this lets it
 * say the sharing ended instead. Memory only: after a restart, a route to a
 * session no longer shared reads as not found.
 */
const seen = new Set<string>();

const key = (baseUrl: string, userId: string, sessionId: string) => JSON.stringify([baseUrl, userId, sessionId]);

export function rememberSharedSessions(baseUrl: string, userId: string, sessionIds: Iterable<string>): void {
	if (!userId) return;
	for (const sessionId of sessionIds) seen.add(key(baseUrl, userId, sessionId));
}

export function sessionWasSharedWithYou(baseUrl: string, userId: string, sessionId: string): boolean {
	return Boolean(userId) && seen.has(key(baseUrl, userId, sessionId));
}

/** For tests. */
export function forgetSharedSessions(): void {
	seen.clear();
}
