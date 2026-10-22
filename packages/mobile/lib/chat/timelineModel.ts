import type {
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationSnapshot,
	ConversationTurn,
} from "./types";
import { stagedAttachmentParts } from "./messageAttachments";

export type ConversationGroup = {
	key: string;
	turnId?: string;
	anchor: number;
	items: ConversationItem[];
	turn?: ConversationTurn;
};

export type ConversationMarker = {
	key: string;
	sequence: number;
	title: string;
	detail?: string;
	state?: ConversationTurn["state"];
};

export type ActivityNode = {
	activity: ConversationActivity;
	children: ActivityNode[];
};

export type ConversationTimelineRenderPlan =
	| { kind: "empty"; inverted: false; groups: [] }
	| { kind: "list"; inverted: boolean; groups: ConversationGroup[] };

export type QueuedConversationMessage = { turnId: string; message: ConversationMessage };

/** Queued turns, except a starting session's opening brief, which reads as sent. */
function queuedTurnIds(snapshot: ConversationSnapshot, openingTurnId?: string): Set<string> {
	const ids = new Set(snapshot.turns.filter((turn) => turn.state === "queued").map((turn) => turn.id));
	if (openingTurnId) ids.delete(openingTurnId);
	return ids;
}

/**
 * Queued prompts stay next to the composer until they are actually dispatched.
 * `openingTurnId` names a starting session's brief: it sits in the timeline
 * above the setup checklist instead, as on the desktop.
 */
export function queuedConversationMessages(snapshot: ConversationSnapshot, openingTurnId?: string): QueuedConversationMessage[] {
	const queued = queuedTurnIds(snapshot, openingTurnId);
	return snapshot.items.flatMap((item) =>
		item.kind === "message" &&
		item.role === "user" &&
		item.origin === "human" &&
		item.turnId &&
		queued.has(item.turnId)
			? [{ turnId: item.turnId, message: item }]
			: [],
	);
}

/** Keep conversation signal while removing provider telemetry/noise. */
export function readableConversationItems(snapshot: ConversationSnapshot, openingTurnId?: string): ConversationItem[] {
	const plannedTurns = new Set(snapshot.turns.filter((turn) => turn.plan?.steps.length).map((turn) => turn.id));
	const queued = queuedTurnIds(snapshot, openingTurnId);
	return snapshot.items.filter((item) => !item.turnId || !queued.has(item.turnId)).filter((item) => item.kind === "message" || (
		item.activityKind !== "usage" &&
		item.activityKind !== "reasoning" &&
		!(item.activityKind === "plan" && item.turnId && plannedTurns.has(item.turnId))
	));
}

/**
 * A turn remains one readable exchange even when queued-message sequencing
 * interleaves it with the turn currently running. This is presentation grouping
 * over daemon-owned sequence/turn identities, never inferred lifecycle state.
 */
export function groupConversationByTurn(
	snapshot: ConversationSnapshot,
	items = readableConversationItems(snapshot),
): ConversationGroup[] {
	const turns = new Map(snapshot.turns.map((turn) => [turn.id, turn]));
	const byTurn = new Map<string, ConversationGroup>();
	const groups: ConversationGroup[] = [];
	for (const item of items) {
		if (!item.turnId) {
			const previous = groups.at(-1);
			if (previous && !previous.turnId) previous.items.push(item);
			else groups.push({ key: `loose-${item.sequence}`, anchor: item.sequence, items: [item] });
			continue;
		}
		const existing = byTurn.get(item.turnId);
		if (existing) existing.items.push(item);
		else {
			// The first loaded item can move backward when an older page arrives. The
			// durable turn identity cannot, so use it as the React/FlatList key and
			// preserve expanded rows and scroll measurements across pagination.
			const group = { key: `turn-${item.turnId}`, turnId: item.turnId, anchor: item.sequence, items: [item], turn: turns.get(item.turnId) } satisfies ConversationGroup;
			byTurn.set(item.turnId, group);
			groups.push(group);
		}
	}
	return groups.sort((left, right) => left.anchor - right.anchor);
}

export type TurnItemPartition = {
	/** Settled turns fold their work behind a "Worked for" header; a running turn shows it inline. */
	settled: boolean;
	/** Only the prompt that opened the turn sits above the status row. */
	prompt: ConversationItem[];
	/** The work between prompt and answer: tool calls, interim prose, later steers. */
	work: ConversationItem[];
	/** Errors and system warnings, which stay readable after the work folds away. */
	notices: ConversationItem[];
	/** The agent's last non-empty reply, shown below the folded work. */
	final: ConversationItem[];
};

function isSteerItem(item: ConversationItem): boolean {
	return item.kind === "activity" && item.activityKind === "system" && item.detail?.event === "steer";
}

function isHumanItem(item: ConversationItem): boolean {
	return (item.kind === "message" && item.role === "user") || isSteerItem(item);
}

function isNoticeItem(item: ConversationItem): boolean {
	if (item.kind !== "activity") return false;
	if (item.activityKind === "error") return true;
	return item.detail?.event !== undefined && !isSteerItem(item) && item.detail.event !== "compaction" && item.activityKind !== "plan";
}

/** Blank assistant messages are the only items the timeline draws nothing for;
 * approval and user-input cards always render, so they fold with the work. */
function rendersNothing(item: ConversationItem): boolean {
	return item.kind === "message" && item.role === "assistant" && item.text.trim() === "";
}

/**
 * Split one turn's items for the Working / Worked presentation, matching desktop.
 * A running turn keeps its work inline under a live "Working" row; once the turn
 * settles the work folds behind "Worked for", leaving the final reply and any
 * notices visible. Queued turns (and turns with no daemon record) stay flat.
 */
export function partitionTurnItems(items: ConversationItem[], turn: ConversationTurn | undefined): TurnItemPartition | undefined {
	if (!turn || turn.state === "queued") return undefined;
	const settled = turn.state !== "running";
	const firstWork = items.findIndex((item) => !isHumanItem(item));
	const leading = firstWork < 0 ? items.length : firstWork;
	const prompt = items.slice(0, leading);
	let rest = items.slice(leading);
	if (!settled) return { settled, prompt, work: rest, notices: [], final: [] };
	let final: ConversationItem[] = [];
	for (let index = rest.length - 1; index >= 0; index -= 1) {
		const item = rest[index];
		if (item.kind === "message" && item.role === "assistant" && item.text.trim() !== "") {
			final = [item];
			rest = rest.filter((_, i) => i !== index);
			break;
		}
	}
	const notices = rest.filter(isNoticeItem);
	const work = rest.filter((item) => !isNoticeItem(item) && !rendersNothing(item));
	return { settled, prompt, work, notices, final };
}

/**
 * Where a visible setup checklist renders: inside the turn that carries the
 * opening brief, or on its own at the live edge when no loaded turn does. It
 * must never go undrawn, because while it shows the screen suppresses the
 * failure and resume banners, and a failed start's Retry lives in it.
 */
export function startupPlacement(groups: ReadonlyArray<Pick<ConversationGroup, "turnId">>, openingTurnId: string | undefined, visible: boolean): "turn" | "standalone" | "none" {
	if (!visible) return "none";
	return openingTurnId && groups.some((group) => group.turnId === openingTurnId) ? "turn" : "standalone";
}

/** Latest-first data lets an inverted virtualized list paint the live edge first. */
export function latestFirstConversationGroups(
	snapshot: ConversationSnapshot,
	items = readableConversationItems(snapshot),
): ConversationGroup[] {
	return groupConversationByTurn(snapshot, items).reverse();
}

export function conversationTimelineRenderPlan(
	snapshot: ConversationSnapshot,
	items = readableConversationItems(snapshot),
	platform: "android" | "ios" = "ios",
): ConversationTimelineRenderPlan {
	// Android's native text-selection toolbar needs untransformed screen
	// coordinates. FlatList's inverted transform detaches it from selected text.
	const inverted = platform !== "android";
	const groups = inverted ? latestFirstConversationGroups(snapshot, items) : groupConversationByTurn(snapshot, items);
	return groups.length === 0
		? { kind: "empty", inverted: false, groups: [] }
		: { kind: "list", inverted, groups };
}

export function conversationMarkers(snapshot: ConversationSnapshot): ConversationMarker[] {
	return groupConversationByTurn(snapshot).map((group) => {
		const human = group.items.find((item) => item.kind === "message" && item.role === "user" && item.origin === "human");
		const assistant = [...group.items].reverse().find((item) => item.kind === "message" && item.role === "assistant" && item.text.trim());
		const activity = group.items.find((item): item is ConversationActivity => item.kind === "activity");
		const title = previewText(human?.kind === "message" ? humanMessageTitle(human.text) : activity?.summary || "Conversation update", 120);
		const detailSource = assistant?.kind === "message" ? assistant.text : activity?.detail?.text || activity?.summary;
		const detail = detailSource ? previewText(String(detailSource), 240) : undefined;
		return { key: group.key, sequence: group.anchor, title, detail: detail && detail !== title ? detail : undefined, state: group.turn?.state };
	});
}

/** A marker names what the human wrote, not the staged-path list AO appended. */
function humanMessageTitle(text: string): string {
	const { body, attachments } = stagedAttachmentParts(text);
	if (attachments.length === 0) return text;
	return body.trim() || (attachments.length === 1 ? "1 attachment" : `${attachments.length} attachments`);
}

export function canRollbackTurn(snapshot: ConversationSnapshot, turn: ConversationTurn): boolean {
	return snapshot.controller.state !== "hibernated" && snapshot.capabilities?.includes("rollback") === true &&
		!snapshot.turns.some((candidate) => candidate.state === "running" || candidate.state === "queued") &&
		turn.state !== "running" && turn.state !== "queued" &&
		Boolean(turn.providerTurnId) &&
		!turn.rolledBack;
}

export function activityStartsExpanded(activity: ConversationActivity): boolean {
	const detail = activity.detail;
	const liveBody = activity.status === "running" && Boolean(
		detail?.output || detail?.result || detail?.error || detail?.patchOutput,
	);
	return activity.status === "failed" || liveBody;
}

/**
 * Reconstruct provider-owned nested agent work without inventing new lifecycle
 * state. Unknown parents and malformed cycles remain visible as roots.
 */
export function activityHierarchy(activities: ConversationActivity[]): ActivityNode[] {
	const byProvider = new Map<string, ActivityNode>();
	const nodes = activities.map((activity) => {
		const node = { activity, children: [] } satisfies ActivityNode;
		if (activity.providerItemId) byProvider.set(activity.providerItemId, node);
		return node;
	});
	const roots: ActivityNode[] = [];
	for (const node of nodes) {
		const parentId = node.activity.detail?.parentProviderItemId;
		const parent = parentId ? byProvider.get(parentId) : undefined;
		if (parent && !activityCycle(node, parent, byProvider)) parent.children.push(node);
		else roots.push(node);
	}
	return roots;
}

export function countActivityNodes(nodes: ActivityNode[]): number {
	return nodes.reduce((count, node) => count + 1 + countActivityNodes(node.children), 0);
}

export function activityNodesRunning(nodes: ActivityNode[]): boolean {
	return nodes.some((node) => node.activity.status === "running" || activityNodesRunning(node.children));
}

function activityCycle(node: ActivityNode, parent: ActivityNode, byProvider: Map<string, ActivityNode>): boolean {
	const visited = new Set<ActivityNode>([node]);
	let current: ActivityNode | undefined = parent;
	while (current) {
		if (visited.has(current)) return true;
		visited.add(current);
		const parentId: string | undefined = current.activity.detail?.parentProviderItemId;
		current = parentId ? byProvider.get(parentId) : undefined;
	}
	return false;
}

function previewText(value: string, limit: number): string {
	const plain = value
		.replace(/```[\s\S]*?```/g, " code sample ")
		.replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
		.replace(/[*_`#>~]+/g, " ")
		.replace(/\s+/g, " ")
		.trim();
	return plain.length > limit ? `${plain.slice(0, limit - 1).trimEnd()}…` : plain;
}
