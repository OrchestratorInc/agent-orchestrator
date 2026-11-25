import type { ConversationSnapshot, ConversationTurn } from "../types/conversation";
import type { RailAskInput, RailSender, RailTurn, RailTurnInput, RailWork } from "./activity-rail";

const SENDER: Record<string, RailSender | undefined> = { human: "you", daemon: "orchestrator", automation: "automation" };

function turnOutcome(turn: ConversationTurn): RailTurn {
	const state = turn.state === "recovered" ? "completed" : turn.state === "interrupted" ? "stopped" : turn.state;
	const started = turn.startedAt ? Date.parse(turn.startedAt) : Number.NaN;
	const ended = turn.completedAt ? Date.parse(turn.completedAt) : Number.NaN;
	const ms = Number.isFinite(started) && Number.isFinite(ended) && ended >= started ? ended - started : undefined;
	const paths = turn.diff && !turn.diff.truncated ? turn.diff.files.map((file) => file.path) : undefined;
	return { state, ms, files: paths?.length, paths, error: state === "failed" ? turn.errorMessage || undefined : undefined };
}

/**
 * What a Chat session's loaded history says the agent was asked and did: one
 * entry per turn that has a request in it, and one for each time the agent
 * stopped for an approval or a question. A turn whose request is above the
 * loaded part of the history, one an undo took back, and one that never ran
 * are left out; the chat has them.
 */
export function sessionRailWork(snapshot: ConversationSnapshot | undefined): RailWork | undefined {
	if (!snapshot) return undefined;
	const turnsById = new Map(snapshot.turns.map((turn) => [turn.id, turn]));
	const turns: RailTurnInput[] = [];
	const asks: RailAskInput[] = [];
	const asked = new Set<string>();
	for (const item of snapshot.items) {
		if (item.kind === "message") {
			const turn = item.turnId ? turnsById.get(item.turnId) : undefined;
			const sender = SENDER[item.origin];
			// The first request in a turn stands for it; a "continue" has no words of its own.
			if (!turn || !sender || item.role !== "user" || item.continuation || asked.has(turn.id) || turn.rolledBack || turn.notRun) continue;
			asked.add(turn.id);
			turns.push({ id: turn.id, at: item.createdAt || turn.requestedAt, sender, text: item.text, turn: turnOutcome(turn) });
		} else if (item.activityKind === "approval" || item.activityKind === "user_input") {
			if (!item.createdAt || item.status === "cancelled") continue;
			asks.push({ id: item.id, at: item.createdAt, kind: item.activityKind === "approval" ? "approval" : "question", summary: item.summary, waiting: item.status === "pending", turnId: item.turnId });
		}
	}
	return { turns, asks };
}
