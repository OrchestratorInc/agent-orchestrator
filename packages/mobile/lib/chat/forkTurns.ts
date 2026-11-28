import type { ConversationTurn } from "./types";

/** A turn a fork can copy: finished, and not rolled back. */
export function forkableTurn(turn: ConversationTurn): boolean {
	return (turn.state === "completed" || turn.state === "recovered") && !turn.rolledBack;
}

/** The latest turn a fork can copy, which every agent can fork from. */
export function latestForkableTurnId(turns: ConversationTurn[]): string | undefined {
	for (let i = turns.length - 1; i >= 0; i--) {
		if (forkableTurn(turns[i])) return turns[i].id;
	}
	return undefined;
}
