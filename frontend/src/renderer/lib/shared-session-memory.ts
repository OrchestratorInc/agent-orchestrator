const seen = new Set<string>();

const key = (baseUrl: string, userId: string, sessionId: string) => JSON.stringify([baseUrl, userId, sessionId]);

export function rememberSharedSessions(baseUrl: string, userId: string, sessionIds: Iterable<string>): void {
	if (!userId) return;
	for (const sessionId of sessionIds) seen.add(key(baseUrl, userId, sessionId));
}

export function sessionWasSharedWithYou(baseUrl: string, userId: string, sessionId: string): boolean {
	return Boolean(userId) && seen.has(key(baseUrl, userId, sessionId));
}

export function forgetSharedSessions(): void {
	seen.clear();
}
