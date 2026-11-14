import { expect, it } from "vitest";
import { forkableTurn, latestForkableTurnId } from "./forkTurns";
import type { ConversationTurn } from "./types";

const turn = (id: string, state: ConversationTurn["state"], rolledBack = false): ConversationTurn => ({ id, state, rolledBack, requestedAt: id });

it("forks only finished turns that weren't rolled back", () => {
	expect(forkableTurn(turn("a", "completed"))).toBe(true);
	expect(forkableTurn(turn("a", "recovered"))).toBe(true);
	expect(forkableTurn(turn("a", "running"))).toBe(false);
	expect(forkableTurn(turn("a", "failed"))).toBe(false);
	expect(forkableTurn(turn("a", "completed", true))).toBe(false);
});

it("finds the latest turn a fork can copy", () => {
	expect(latestForkableTurnId([turn("1", "completed"), turn("2", "completed"), turn("3", "running")])).toBe("2");
	expect(latestForkableTurnId([turn("1", "running")])).toBeUndefined();
});
