import type { components } from "../../../api/schema";
import type {
	ControllerState,
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationSnapshot,
	ConversationTurn,
	TurnState,
} from "../../types/conversation";

type SideSnapshot = components["schemas"]["SideSnapshot"];
type SideTurn = components["schemas"]["SideTurn"];
type SideMessage = components["schemas"]["SideMessage"];
type SideActivity = components["schemas"]["SideActivity"];

const ACTIVITY_KINDS: ReadonlySet<string> = new Set([
	"command",
	"file_change",
	"plan",
	"reasoning",
	"approval",
	"usage",
	"error",
	"system",
	"mcp_tool",
	"auto_review",
	"user_input",
]);

const TURN_STATES: ReadonlySet<string> = new Set([
	"queued",
	"running",
	"completed",
	"recovered",
	"interrupted",
	"failed",
	"cancelled",
]);

/** A side message in the main chat's shape; user turns carry their excerpts. */
export function sideConversationMessage(message: SideMessage, turns: readonly SideTurn[]): ConversationMessage {
	const user = message.role === "user";
	return {
		kind: "message",
		id: message.id,
		turnId: message.turnId,
		sequence: message.sequence,
		revision: message.revision,
		role: user ? "user" : "assistant",
		origin: user ? "human" : "provider",
		text: message.text,
		streaming: message.streaming,
		createdAt: message.createdAt,
		content: user
			? (turns.find((turn) => turn.id === message.turnId)?.references ?? []).map((ref) => ({
					type: "excerpt",
					text: ref.selection,
					sourceConversationId: ref.conversationId,
					sourceMessageId: ref.messageId,
					sourceRevision: ref.revision,
				}))
			: undefined,
	};
}

/** A side activity in the main chat's shape. */
export function sideConversationActivity(activity: SideActivity): ConversationActivity {
	const kind = activity.kind === "input" ? "user_input" : activity.kind;
	const detail =
		activity.kind === "command" && activity.text
			? {
					...(activity.detail && typeof activity.detail === "object" && !Array.isArray(activity.detail)
						? activity.detail
						: {}),
					output: activity.text,
					// The detail's own copy of the output may have been cut; this one was not.
					outputTruncated: undefined,
				}
			: activity.detail;
	return {
		kind: "activity",
		id: activity.id,
		turnId: activity.turnId,
		sequence: 0,
		revision: 1,
		activityKind: (ACTIVITY_KINDS.has(kind) ? kind : "system") as ConversationActivity["activityKind"],
		status: activity.status as ConversationActivity["status"],
		summary: activity.summary || activity.kind,
		detail: detail as ConversationActivity["detail"],
		requestId: activity.requestId,
		decisions: activity.decisions?.map((decision) => ({
			id: decision.id,
			label: decision.label,
			kind: decision.kind as NonNullable<ConversationActivity["decisions"]>[number]["kind"],
		})),
		createdAt: activity.createdAt,
	};
}

function sideConversationTurn(turn: SideTurn): ConversationTurn {
	const state = (TURN_STATES.has(turn.state) ? turn.state : "failed") as TurnState;
	return {
		id: turn.id,
		state,
		errorMessage: turn.errorMessage || undefined,
		// A side retry re-runs the same turn and records itself as its source. The
		// Timeline would read that as "already retried" and drop Retry, stranding a
		// turn that fails again with the side's queue blocked behind it.
		retryOfTurnId: turn.retryOfTurnId && turn.retryOfTurnId !== turn.id ? turn.retryOfTurnId : undefined,
		// The main timeline offers Retry only on a failed turn the provider saw.
		// Every failed side turn is retryable through the side API, including one
		// that failed before dispatch, so mark it as seen.
		providerTurnId: state === "failed" ? turn.id : undefined,
		requestedAt: turn.createdAt,
		startedAt: turn.startedAt ?? undefined,
		completedAt: turn.completedAt ?? undefined,
	};
}

function controllerState(side: SideSnapshot["side"], running: boolean): ControllerState {
	switch (side.state) {
		case "ready":
			return running ? "busy" : "ready";
		case "opening":
			return "connecting";
		case "recovering":
			return "recovering";
		default:
			return "stopped";
	}
}

/**
 * The side transcript as a main-chat snapshot, so the side panel renders it
 * with the main chat's own Timeline. Queued turns are left out: like the main
 * chat, they live in the queue dock until they dispatch.
 */
export function sideConversationSnapshot(snapshot: SideSnapshot, olderPages: readonly SideSnapshot[] = []): ConversationSnapshot {
	const pages = [...olderPages].reverse().concat(snapshot);
	const turns = dedupe(pages.flatMap((page) => page.turns ?? []));
	const queued = new Set(turns.filter((turn) => turn.state === "queued").map((turn) => turn.id));
	const entries = [
		...dedupe(pages.flatMap((page) => page.messages ?? []))
			.filter((message) => !queued.has(message.turnId))
			.map((message) => ({ time: message.createdAt, order: message.sequence, item: sideConversationMessage(message, turns) as ConversationItem })),
		...dedupe(pages.flatMap((page) => page.activities ?? []))
			.filter((activity) => !queued.has(activity.turnId))
			.map((activity) => ({ time: activity.createdAt, order: 0, item: sideConversationActivity(activity) as ConversationItem })),
	].sort((a, b) => a.time.localeCompare(b.time) || a.order - b.order);
	// The Timeline trusts snapshot order and keys groups and runs on sequence.
	// Side activities have no sequence of their own, so derive one from time:
	// unlike a running index it does not shift when older pages load or a queued
	// turn dispatches, so turns are not remounted and open sections stay open.
	let previous = -Infinity;
	const items = entries.map(({ time, item }) => {
		const at = Date.parse(time);
		// Same-millisecond entries keep their sorted order with a small step.
		previous = at > previous ? at : previous + 0.001;
		return { ...item, sequence: previous };
	});
	const running = turns.some((turn) => turn.state === "running");
	return {
		conversationId: snapshot.side.id,
		sessionId: snapshot.side.sessionId,
		harness: "",
		mode: "chat",
		controller: { state: controllerState(snapshot.side, running), error: snapshot.side.errorMessage || undefined },
		turns: turns.filter((turn) => !queued.has(turn.id)).map(sideConversationTurn),
		items,
		latestSequence: items.at(-1)?.sequence ?? 0,
		oldestSequence: items[0]?.sequence ?? 0,
		hasMoreBefore: olderPages.at(-1)?.hasMore ?? snapshot.hasMore,
		settings: { model: snapshot.side.model, reasoningEffort: snapshot.side.effort },
	};
}

function dedupe<T extends { id: string }>(entries: T[]): T[] {
	const seen = new Map<string, T>();
	// Later pages are newer, so the last copy of an entry wins.
	for (const entry of entries) seen.set(entry.id, entry);
	return [...seen.values()];
}
