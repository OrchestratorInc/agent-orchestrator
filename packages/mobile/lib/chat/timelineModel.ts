import type {
	ActivityDetail,
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationSnapshot,
	ConversationPlan,
	ConversationTurn,
} from "./types";

export type ConversationGroup = {
	key: string;
	turnId?: string;
	anchor: number;
	items: ConversationItem[];
	turn?: ConversationTurn;
	/** The turn's plan when this turn should show it; see attachCurrentPlans. */
	plan?: ConversationPlan;
	/** The group opens at a steer, not at the turn's prompt (see splitAtSteers). */
	continuesTurn?: boolean;
	/**
	 * False on the part of a turn a later steer cut off: its `turn` is a settled
	 * view of just that stretch, and the turn's actions, diff and plan draw under
	 * the last part. Unset on groups that are not a turn.
	 */
	endsTurn?: boolean;
	/**
	 * The daemon's record of the turn, which `turn` only presents: a part a later
	 * steer cut off reads as settled while the real turn may still run, and its
	 * rows' statuses follow the real one (settledTurnItems).
	 */
	sourceTurn?: ConversationTurn;
};

export type ActivityNode = {
	activity: ConversationActivity;
	children: ActivityNode[];
};

export type ConversationTimelineRenderPlan =
	| { kind: "empty"; groups: [] }
	| { kind: "list"; groups: ConversationGroup[] };

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
		// The model picker already names the model that answered.
		item.detail?.event !== "model.rerouted" &&
		!(item.activityKind === "plan" && item.turnId && plannedTurns.has(item.turnId)) &&
		// An older copy of a list that spans turns; a later turn or run carries it.
		!(item.activityKind === "plan" && item.detail?.superseded)
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
			const turn = turns.get(item.turnId);
			const group = { key: `turn-${item.turnId}`, turnId: item.turnId, anchor: turn?.anchorSequence ?? item.sequence, items: [item], turn } satisfies ConversationGroup;
			byTurn.set(item.turnId, group);
			groups.push(group);
		}
	}
	groups.sort((left, right) => left.anchor - right.anchor);
	attachCurrentPlans(groups);
	return groups.flatMap(splitAtSteers);
}

/**
 * A steer is the person speaking, so it opens a new visual turn even though the
 * provider keeps running the same one, as on the desktop: the work before it
 * settles into its own "Worked for" row instead of folding the steer away with
 * it, and the steer starts a fresh Working row and reply. Only the last part
 * carries the real turn's live state, outcome, actions and plan. A steer another
 * session or a labelled automation sent stays in the work (isAutomationSteer).
 */
function splitAtSteers(group: ConversationGroup): ConversationGroup[] {
	const turn = group.turn;
	if (!turn) return [group];
	if (turn.state === "queued") return [{ ...group, endsTurn: true, sourceTurn: turn }];
	const starts = group.items.flatMap((item, index) => isHumanSteer(item) ? [index] : []);
	if (starts.length === 0) return [{ ...group, endsTurn: true, sourceTurn: turn }];
	// A loaded page can open on a steer, the prompt above it not loaded yet. That
	// part is the steer's, keyed and timed by it, so loading the prompt later
	// adds the opening part above it and moves no part's key.
	const bounds = starts[0] === 0 ? [...starts, group.items.length] : [0, ...starts, group.items.length];
	const opens = (from: number) => from === 0 && starts[0] !== 0;
	const startedAt = (from: number) => opens(from) ? turn.startedAt ?? turn.requestedAt : group.items[from].createdAt;
	return bounds.slice(0, -1).map((from, index) => {
		const to = bounds[index + 1];
		const items = group.items.slice(from, to);
		const last = to === group.items.length;
		const part: ConversationGroup = {
			key: opens(from) ? group.key : `${group.key}:steer:${items[0].id}`,
			turnId: group.turnId,
			anchor: index === 0 ? group.anchor : items[0].sequence,
			items,
			turn: segmentTurn(turn, last, startedAt(from), last ? undefined : startedAt(to)),
			endsTurn: last,
			sourceTurn: turn,
		};
		if (!opens(from)) part.continuesTurn = true;
		if (last && group.plan) part.plan = group.plan;
		return part;
	});
}

/**
 * The derived turn views, per source turn object. A part's view is the same
 * object for as long as its turn record is, so useStableGroups keeps the part
 * and its memoized render while another turn streams.
 */
const segmentTurns = new WeakMap<ConversationTurn, Map<string, ConversationTurn>>();

function segmentTurn(turn: ConversationTurn, last: boolean, startedAt: string | undefined, endedAt: string | undefined): ConversationTurn {
	const key = last ? `last\u0000${startedAt}` : `${startedAt}\u0000${endedAt}`;
	let views = segmentTurns.get(turn);
	if (!views) segmentTurns.set(turn, views = new Map());
	let view = views.get(key);
	if (!view) {
		view = last
			? { ...turn, startedAt }
			: { id: turn.id, state: "completed", requestedAt: turn.requestedAt, startedAt, completedAt: endedAt, hasHumanPrompt: turn.hasHumanPrompt };
		views.set(key, view);
	}
	return view;
}

/** A steer the person typed: not one another session or a labelled automation sent. */
function isHumanSteer(item: ConversationItem): boolean {
	return item.kind === "activity" && isSteerItem(item) && !isAutomationSteer(item.detail);
}

/**
 * Whether another agent sent this steer: an AO session that names itself, or an
 * automation with a label (a Cloud orchestrator's message steered into a busy
 * worker names only its label). As on the desktop's SteerMessage. Such a steer
 * stays in the work, and its row is drawn as its sender's.
 */
export function isAutomationSteer(detail: ActivityDetail | undefined): boolean {
	return Boolean(detail?.senderSessionId?.trim()) || (detail?.origin === "automation" && Boolean(detail.senderDisplayName?.trim()));
}

/**
 * Gives each turn group the plan it should show, as the desktop does: the turn's
 * plan, unless the daemon marked it superseded. That is an older copy of a list the
 * agent keeps across turns (Claude's task list), which a later turn or run carries.
 */
function attachCurrentPlans(groups: ConversationGroup[]): void {
	for (const group of groups) {
		const plan = group.turn?.plan;
		if (plan?.steps.length && !plan.superseded) group.plan = plan;
	}
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
 * A finished turn's items as the timeline draws them. Older daemons store the
 * provider's failure notice ("You've hit your 5-hour limit…") again as a running
 * row each time the agent restarts and replays its history, after the turn has
 * long ended, so it would spin forever and repeat. On a finished turn nothing
 * runs: a leftover running row shows the status the daemon gives such rows when
 * the turn settles, a notice that restates the turn's own error is dropped (the
 * turn's outcome already says it), and replayed copies of a notice show once.
 */
export function settledTurnItems(items: ConversationItem[], turn: ConversationTurn | undefined): ConversationItem[] {
	if (!turn || turn.state === "running" || turn.state === "queued") return items;
	const turnError = turn.state === "failed" ? turn.errorMessage?.trim() : undefined;
	const notices = new Set<string>();
	const settled: ConversationItem[] = [];
	for (const item of items) {
		if (item.kind !== "activity") {
			settled.push(item);
			continue;
		}
		if (item.detail?.event === "provider.failure") {
			const summary = String(item.summary ?? "").trim();
			if (turnError && summary === turnError) continue;
			// A replay repeats the whole notice; one that shares only the title is new.
			const key = [summary, item.detail.category ?? "", item.detail.severity ?? "", item.detail.text ?? ""].join("\u0000");
			if (notices.has(key)) continue;
			notices.add(key);
		}
		settled.push(item.status === "running" ? { ...item, status: settledActivityStatus(turn.state) } : item);
	}
	return settled;
}

/** The daemon's terminalActivityStatus: what a settling turn writes over a running row. */
function settledActivityStatus(state: ConversationTurn["state"]): ConversationActivity["status"] {
	if (state === "completed" || state === "recovered") return "completed";
	if (state === "interrupted") return "cancelled";
	return "failed";
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

/**
 * The conversation in reading order, oldest first, on both platforms. The list
 * is never inverted: Android's text-selection toolbar needs untransformed
 * screen coordinates, and iOS 26's navigation bar blurs the list's top edge,
 * which an inverted list turns into its composer end.
 */
export function conversationTimelineRenderPlan(
	snapshot: ConversationSnapshot,
	items = readableConversationItems(snapshot),
): ConversationTimelineRenderPlan {
	const groups = groupConversationByTurn(snapshot, items);
	return groups.length === 0 ? { kind: "empty", groups: [] } : { kind: "list", groups };
}

/**
 * Human prompts the daemon can branch before and resend edited, mirroring the
 * desktop's gate: an idle controller, a settled turn, and either the binding's
 * first prompt, a native fork point, or replayable history. `reconstructed`
 * names prompts whose earlier context would be replayed rather than forked.
 */
export function editableHumanTurns(snapshot: ConversationSnapshot): { editable: Set<string>; reconstructed: Set<string> } {
	const editable = new Set<string>();
	const reconstructed = new Set<string>();
	if (snapshot.controller.state !== "ready") return { editable, reconstructed };
	const settled = new Set(snapshot.turns
		.filter((turn) => turn.state === "completed" || turn.state === "interrupted" || turn.state === "failed")
		.map((turn) => turn.id));
	const prompts = snapshot.items.filter((item): item is ConversationMessage =>
		item.kind === "message" && item.role === "user" && item.origin === "human" && Boolean(item.turnId));
	const firstTurnId = prompts.find((item) => item.editAvailable)?.turnId;
	const forkAfter = snapshot.nativeForkAvailableAfterSequence ?? 0;
	const canFork = snapshot.capabilities?.includes("fork") === true;
	const canReplay = snapshot.capabilities?.includes("prompt_replay") === true && snapshot.capabilities.includes("embedded_context");
	for (const item of prompts) {
		const turnId = item.turnId as string;
		if (!item.editAvailable || !settled.has(turnId)) continue;
		const first = !snapshot.hasMoreBefore && turnId === firstTurnId;
		const nativeFork = canFork && forkAfter > 0 && item.sequence > forkAfter;
		if (!first && !nativeFork && !canReplay) continue;
		editable.add(turnId);
		if (!first && !nativeFork) reconstructed.add(turnId);
	}
	return { editable, reconstructed };
}

/** Whether two editable-turn results name the same turns, so a caller can keep the older identity. */
export function sameEditableTurns(
	a: ReturnType<typeof editableHumanTurns> | undefined,
	b: ReturnType<typeof editableHumanTurns> | undefined,
): boolean {
	if (!a || !b) return a === b;
	const same = (x: ReadonlySet<string>, y: ReadonlySet<string>) => x.size === y.size && [...x].every((id) => y.has(id));
	return same(a.editable, b.editable) && same(a.reconstructed, b.reconstructed);
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

/**
 * What a compaction row says, in the desktop's words: under way, could not run
 * (with why, or that it was stopped), or done with what it freed and how full
 * the context is now.
 */
export function compactionLine(activity: ConversationActivity): string {
	if (activity.status === "running") return "Compacting conversation";
	if (activity.status === "failed" || activity.status === "cancelled") {
		const reason = activity.detail?.error || (activity.status === "cancelled" ? "Stopped" : "");
		return reason ? `Couldn't compact conversation · ${reason}` : "Couldn't compact conversation";
	}
	const reclaimed = activity.detail?.tokensReclaimed;
	const after = activity.detail?.tokensAfter;
	const window = activity.detail?.contextWindow;
	return `Compacted conversation${reclaimed ? ` · −${formatTokens(reclaimed)}` : ""}${after && window ? ` · ${Math.round((after / window) * 100)}% full` : ""}`;
}

function formatTokens(value: number): string { return value >= 1_000 ? `${(value / 1_000).toFixed(value >= 10_000 ? 0 : 1)}k` : String(value); }

export function summarizeActivities(activities: ConversationActivity[]): string {
	let reads = 0;
	let searches = 0;
	let vcs = 0;
	let commands = 0;
	let tools = 0;
	let reviews = 0;
	let plans = 0;
	let actions = 0;
	for (const activity of activities) {
		if (activity.activityKind === "ao_action") { actions++; continue; }
		if (activity.activityKind === "mcp_tool") { tools++; continue; }
		if (activity.activityKind === "auto_review") { reviews++; continue; }
		if (activity.activityKind === "plan") { plans++; continue; }
		switch (commandCategory(activity.detail?.command ?? activity.summary)) {
			case "read": reads++; break;
			case "search": searches++; break;
			case "vcs": vcs++; break;
			default: commands++;
		}
	}
	const parts: string[] = [];
	if (reads) parts.push(`${reads} ${reads === 1 ? "file" : "files"}`);
	if (searches) parts.push(`${searches} ${searches === 1 ? "search" : "searches"}`);
	if (vcs) parts.push(`${vcs} git ${vcs === 1 ? "check" : "checks"}`);
	if (commands) parts.push(`${commands} ${commands === 1 ? "command" : "commands"}`);
	if (tools) parts.push(`${tools} tool ${tools === 1 ? "call" : "calls"}`);
	if (reviews) parts.push(`${reviews} auto-${reviews === 1 ? "decision" : "decisions"}`);
	if (plans) parts.push("updated plan");
	if (actions) parts.push(`${actions} ${actions === 1 ? "action" : "actions"}`);
	const verb = reads || searches ? "Explored" : actions && parts.length === 1 ? "Performed" : "Ran";
	return `${verb} ${parts.length ? parts.join(", ") : `${activities.length} steps`}`;
}

function commandCategory(text: string): "read" | "search" | "vcs" | "run" {
	const head = text.trim().split(/\s+/, 1)[0] ?? "";
	const binary = head.slice(head.lastIndexOf("/") + 1);
	if (READ_COMMANDS.has(binary)) return "read";
	if (SEARCH_COMMANDS.has(binary)) return "search";
	if (binary === "git" || binary === "gh") return "vcs";
	return "run";
}

const READ_COMMANDS = new Set(["cat", "sed", "nl", "head", "tail", "bat", "less", "more", "wc", "jq"]);
const SEARCH_COMMANDS = new Set(["rg", "grep", "find", "fd", "ls", "tree", "glob", "ag"]);

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
