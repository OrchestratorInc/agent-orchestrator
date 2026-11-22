import type { WorkspaceSession } from "../types/workspace";
import { sessionBranchPresentation } from "./session-branch";

/**
 * What a board search matches on a card, lowercased: its title, session id,
 * the branch the card shows (a provisional branch stays hidden, as on the
 * card) and its PR numbers as "#123".
 */
export function boardSearchText(session: WorkspaceSession): string {
	return [
		session.title,
		session.id,
		sessionBranchPresentation(session).branch,
		...session.prs.map((pr) => `#${pr.number}`),
	]
		.filter(Boolean)
		.join("\n")
		.toLowerCase();
}

/** The query's words; empty when there is nothing to search for. */
export function boardSearchTokens(query: string): string[] {
	return query.trim().toLowerCase().split(/\s+/).filter(Boolean);
}

/** Sessions whose search text holds every token, in their original order. */
export function filterBoardSessions<T extends WorkspaceSession>(sessions: T[], query: string): T[] {
	const tokens = boardSearchTokens(query);
	if (tokens.length === 0) return sessions;
	return sessions.filter((session) => {
		const text = boardSearchText(session);
		return tokens.every((token) => text.includes(token));
	});
}
