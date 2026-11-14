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
					annotationId: ref.id, annotationOrder: ref.order,
					text: ref.selection,
					comment: ref.comment || undefined,
					sourceConversationId: ref.conversationId,
					sourceMessageId: ref.messageId,
					sourceRevision: ref.revision, snapshot: ref.snapshot,
				}))
			: undefined,
	};
}

export function sideConversationActivity(activity: SideActivity): ConversationActivity {
	const kind = activity.kind === "input" ? "user_input" : activity.kind;
	const detail =
		activity.kind === "command" && activity.text
			? {
					...(activity.detail && typeof activity.detail === "object" && !Array.isArray(activity.detail)
						? activity.detail
						: {}),
					output: activity.text,
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
		retryOfTurnId: turn.retryOfTurnId && turn.retryOfTurnId !== turn.id ? turn.retryOfTurnId : undefined,
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
	let previous = -Infinity;
	const items = entries.map(({ time, item }) => {
		const at = Date.parse(time);
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
	for (const entry of entries) seen.set(entry.id, entry);
	return [...seen.values()];
}
