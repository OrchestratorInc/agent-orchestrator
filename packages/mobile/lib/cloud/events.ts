import type { ClientEvent, CloudClient, Session } from "@aoagents/cloud-client";
import type { ActivityDetail, ActivityKind, ActivityStatus, ConversationActivity, ConversationItem, ConversationMessage, ConversationTurn, FileChange } from "../chat/types";

function payloadField(event: ClientEvent, field: string): unknown {
	const payload = event.payload as Record<string, unknown> | undefined;
	return payload?.[field];
}
function eventTurnId(event: ClientEvent): string | undefined {
	const value = payloadField(event, "turnId");
	return typeof value === "string" && value ? value : undefined;
}
function eventAttempt(event: ClientEvent): number | undefined {
	const value = payloadField(event, "attempt");
	return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}
const endedTypes = new Set(["chat.turn_completed", "chat.turn_interrupted", "chat.turn_aborted"]);

/** Only a claimed turn or real provider output establishes running state. */
export function toConversationTurns(events: readonly ClientEvent[]): ConversationTurn[] {
	const turns = new Map<string, ConversationTurn>();
	const attempts = new Map<string, number>();
	for (const event of events) {
		const id = eventTurnId(event);
		if (!id) continue;
		const attempt = eventAttempt(event);
		if (attempt !== undefined && attempt < (attempts.get(id) ?? 0)) continue;
		if (attempt !== undefined) attempts.set(id, attempt);
		let turn = turns.get(id);
		if (!turn) { turn = { id, state: "queued", requestedAt: event.createdAt }; turns.set(id, turn); }
		if (event.type === "chat.turn_started" || (event.type === "chat.assistant_delta" && turn.state === "queued")) {
			turn.state = "running"; turn.startedAt = event.createdAt;
			turn.completedAt = undefined; turn.errorMessage = undefined;
		} else if (endedTypes.has(event.type)) {
			turn.state = event.type === "chat.turn_completed" ? "completed" : event.type === "chat.turn_interrupted" ? "interrupted" : "failed";
			turn.completedAt = event.createdAt;
			const error = payloadField(event, "error");
			if (typeof error === "string") turn.errorMessage = error;
		}
		if (event.type === "chat.activity") {
			const raw = payloadField(event, "activity");
			if (!raw || typeof raw !== "object") continue;
			const activity = raw as Record<string, unknown>;
			const detail = activity.detail && typeof activity.detail === "object" ? activity.detail as Record<string, unknown> : {};
			if (activity.kind === "turn_diff" && typeof detail.diff === "string") turn.diff = { files: cloudTurnDiff(detail.diff), truncated: detail.truncated === true };
			if (activity.kind === "plan") {
				const safe = activityDetail(detail);
				if (safe.steps) turn.plan = { explanation: safe.explanation, steps: safe.steps };
			}
		}
	}
	return [...turns.values()];
}

const activityKinds = new Set<ActivityKind>(["command", "file_change", "plan", "reasoning", "approval", "usage", "error", "system", "mcp_tool", "auto_review", "user_input"]);
const activityStatuses = new Set<ActivityStatus>(["running", "completed", "failed", "cancelled", "pending", "resolved", "recovered"]);
function activityDetail(raw: Record<string, unknown>): ActivityDetail {
	const detail: ActivityDetail = { ...raw };
	// Provider payloads are not renderer types: malformed display values must
	// not become React children or commands, even for unknown activity kinds.
	for (const key of ["text", "command", "rawCommand", "cwd", "reason", "terminalInput", "providerToolName", "providerTitle", "server", "toolName", "namespace", "error", "progress", "explanation", "message", "url"]) {
		if (detail[key] !== undefined && typeof detail[key] !== "string") delete detail[key];
	}
	if (Array.isArray(raw.steps)) detail.steps = raw.steps.filter((step): step is { text: string; status: "pending" | "in_progress" | "completed" } =>
		!!step && typeof step === "object" && typeof step.text === "string" && ["pending", "in_progress", "completed"].includes(step.status));
	else delete detail.steps;
	if (Array.isArray(raw.files)) detail.files = raw.files.flatMap((file): Array<string | FileChange> => {
		if (typeof file === "string") return [file];
		if (!file || typeof file !== "object" || typeof file.path !== "string") return [];
		return [{ ...file, status: ["added", "modified", "deleted", "renamed"].includes(file.status) ? file.status : "modified",
			additions: Number.isFinite(file.additions) ? file.additions : 0, deletions: Number.isFinite(file.deletions) ? file.deletions : 0,
			patch: typeof file.patch === "string" ? file.patch : undefined }];
	}) as FileChange[] | string[];
	else delete detail.files;
	return detail;
}

function cloudTurnDiff(patch: string): FileChange[] {
	const files: FileChange[] = [];
	let current: FileChange | undefined;
	let inHunk = false;
	for (const line of patch.replace(/\n$/, "").split("\n")) {
		if (line.startsWith("diff --git ")) {
			if (current) files.push(current);
			const path = line.match(/^diff --git a\/.+ b\/(.+)$/)?.[1];
			current = path ? { path, status: "modified", additions: 0, deletions: 0, patch: line + "\n" } : undefined;
			inHunk = false;
		} else if (current) {
			current.patch += line + "\n";
			if (line.startsWith("new file mode")) current.status = "added";
			else if (line.startsWith("deleted file mode")) current.status = "deleted";
			else if (line.startsWith("rename from ")) { current.status = "renamed"; current.oldPath = line.slice(12); }
			else if (line.startsWith("rename to ")) current.path = line.slice(10);
			else if (line.startsWith("+++ b/")) current.path = line.slice(6);
			else if (line.startsWith("@@")) inHunk = true;
			else if (inHunk && line.startsWith("+")) current.additions++;
			else if (inHunk && line.startsWith("-")) current.deletions++;
		}
	}
	if (current) files.push(current);
	return files;
}

/** Replay into the app's existing rich timeline, fenced by turn and attempt. */
export function toConversationItems(events: readonly ClientEvent[]): ConversationItem[] {
	const items: ConversationItem[] = [];
	const open = new Map<string, ConversationMessage>();
	const activities = new Map<string, ConversationActivity>();
	const approvals = new Map<string, ConversationActivity>();
	const patchesByAttempt = new Map<string, FileChange[]>();
	const attempts = new Map<string, number>();
	const settle = (turnId?: string, attempt?: number) => {
		for (const [key, message] of open) {
			const identity = JSON.parse(key) as [string, number | string, string];
			if ((!turnId || message.turnId === turnId) && (attempt === undefined || identity[1] === attempt || identity[1] === "legacy")) {
				message.streaming = false; message.revision++; open.delete(key);
			}
		}
		for (const activity of [...activities.values(), ...approvals.values()]) {
			if ((!turnId || activity.turnId === turnId) && (attempt === undefined || activity.attempt === undefined || activity.attempt === attempt) && ["running", "pending"].includes(activity.status)) {
				activity.status = "cancelled"; activity.revision++;
			}
		}
	};
	for (const event of events) {
		const turnId = eventTurnId(event);
		const attempt = eventAttempt(event);
		const previousAttempt = turnId ? attempts.get(turnId) : undefined;
		if (turnId && attempt !== undefined) {
			if (attempt < (previousAttempt ?? 0)) continue;
			if (previousAttempt !== undefined && attempt > previousAttempt) settle(turnId, previousAttempt);
			attempts.set(turnId, attempt);
		}
		if (event.type === "chat.user_message") {
			// Unscoped legacy sends close legacy streams. A newly queued turn
			// must not prematurely settle another turn's active reply.
			if (!turnId) settle();
			const text = payloadField(event, "text");
			if (typeof text !== "string" || !text) continue;
			const automation = payloadField(event, "origin") === "automation";
			const display = payloadField(event, "displayText");
			const stringField = (name: string) => { const value = payloadField(event, name); return typeof value === "string" ? value : undefined; };
			items.push({ kind: "message", id: event.sessionId + ":" + event.sequence, sequence: event.sequence, revision: 1,
				turnId, role: "user", origin: automation ? "automation" : "human", text: automation && typeof display === "string" ? display : text,
				senderLabel: automation ? stringField("senderLabel") : undefined, senderSessionId: automation ? stringField("senderSessionId") : undefined,
				senderProjectId: automation ? stringField("senderProjectId") : undefined, senderDisplayName: automation ? stringField("senderDisplayName") : undefined,
				streaming: false, delivery: "accepted", createdAt: event.createdAt });
		} else if (event.type === "chat.assistant_delta") {
			const text = payloadField(event, "text");
			if (typeof text !== "string" || !text) continue;
			const itemId = payloadField(event, "itemId");
			const key = JSON.stringify([turnId ?? "without-turn", attempt ?? "legacy", typeof itemId === "string" && itemId ? itemId : "legacy"]);
			const previous = open.get(key);
			if (previous) { previous.text += text; previous.revision++; }
			else {
				const message: ConversationMessage = { kind: "message", id: "cloud-assistant-" + JSON.stringify([event.sessionId, key, event.sequence]),
					sequence: event.sequence, revision: 1, turnId, attempt, role: "assistant", origin: "provider", text, streaming: true, createdAt: event.createdAt };
				open.set(key, message); items.push(message);
			}
		} else if (endedTypes.has(event.type)) {
			settle(turnId, attempt);
			const error = payloadField(event, "error");
			if (event.type === "chat.turn_aborted" && typeof error === "string" && error) items.push({
				kind: "activity", id: "cloud-error-" + event.sequence, turnId, attempt, sequence: event.sequence, revision: 1,
				activityKind: "error", status: "failed", summary: error, detail: { error }, createdAt: event.createdAt });
		} else if (event.type === "chat.activity") {
			const raw = payloadField(event, "activity");
			if (!raw || typeof raw !== "object") continue;
			const value = raw as Record<string, unknown>;
			if (typeof value.id !== "string" || !value.id || typeof value.kind !== "string") continue;
			if (value.kind === "turn_diff") {
				const detail = value.detail && typeof value.detail === "object" ? value.detail as Record<string, unknown> : {};
				if (typeof detail.diff === "string") patchesByAttempt.set(JSON.stringify([turnId, attempt ?? "legacy"]), cloudTurnDiff(detail.diff));
				continue;
			}
			const detail = activityDetail(value.detail && typeof value.detail === "object" ? value.detail as Record<string, unknown> : {});
			const key = JSON.stringify([event.sessionId, turnId ?? "without-turn", attempt ?? "legacy", value.id]);
			const previous = activities.get(key);
			const kind = activityKinds.has(value.kind as ActivityKind) ? value.kind as ActivityKind : "system";
			const status = activityStatuses.has(value.status as ActivityStatus) ? value.status as ActivityStatus : "completed";
			const summary = typeof value.summary === "string" ? value.summary : "Agent activity";
			const merged = { ...previous?.detail, ...detail };
			if (typeof detail.textDelta === "string") merged.text = (previous?.detail?.text ?? "") + detail.textDelta;
			if (typeof detail.outputDelta === "string") {
				merged.output = (typeof previous?.detail?.output === "string" ? previous.detail.output : "") + detail.outputDelta;
				merged.outputSource = "stream"; merged.outputMayBePartial = true;
			}
			if (previous) { previous.activityKind = kind; previous.status = status; previous.summary = summary; previous.detail = merged; previous.revision++; }
			else {
				const activity: ConversationActivity = { kind: "activity", id: "cloud-activity-" + key, turnId, attempt, providerItemId: value.id,
					sequence: event.sequence, revision: 1, activityKind: kind, status, summary, detail: merged, createdAt: event.createdAt };
				activities.set(key, activity); items.push(activity);
			}
		} else if (event.type === "chat.approval_requested") {
			const requestId = payloadField(event, "requestId");
			if (typeof requestId !== "string" || !requestId) continue;
			const key = JSON.stringify([turnId, attempt ?? "legacy", requestId]);
			const raw = payloadField(event, "decisions");
			const decisions = Array.isArray(raw) ? raw.filter((item): item is { id: string; label: string } =>
				!!item && typeof item === "object" && typeof item.id === "string" && !!item.id && typeof item.label === "string") : [];
			const summary = payloadField(event, "summary");
			const activity: ConversationActivity = { kind: "activity", id: "cloud-approval-" + key, turnId, attempt,
				sequence: event.sequence, revision: 1, activityKind: "approval", status: "pending",
				summary: typeof summary === "string" ? summary : "Permission required", requestId, decisions,
				detail: { toolKind: typeof payloadField(event, "toolKind") === "string" ? payloadField(event, "toolKind") : undefined }, createdAt: event.createdAt };
			const previous = approvals.get(key);
			if (previous) Object.assign(previous, { ...activity, revision: previous.revision + 1 });
			else { approvals.set(key, activity); items.push(activity); }
		} else if (event.type === "chat.approval_decided") {
			const requestId = payloadField(event, "requestId");
			const activity = [...approvals.values()].reverse().find((item) => item.requestId === requestId && (!turnId || item.turnId === turnId));
			if (activity) { activity.status = "completed"; activity.revision++; activity.detail = { ...activity.detail, decision: payloadField(event, "decision") }; }
		} else if (event.type === "chat.turn_steered") {
			const text = payloadField(event, "text");
			if (typeof text === "string" && text) items.push({ kind: "activity", id: "cloud-steer-" + event.sequence, turnId, sequence: event.sequence, revision: 1,
				activityKind: "system", status: "completed", summary: "Steered: " + text,
				detail: { event: "steer", text, origin: "human", clientMessageId: typeof payloadField(event, "clientMessageId") === "string" ? payloadField(event, "clientMessageId") as string : undefined }, createdAt: event.createdAt });
		}
	}
	for (const activity of activities.values()) {
		if (activity.activityKind !== "file_change" || !Array.isArray(activity.detail?.files)) continue;
		const patches = patchesByAttempt.get(JSON.stringify([activity.turnId, activity.attempt ?? "legacy"]));
		if (activity.detail.files.every((file): file is FileChange => typeof file !== "string")) {
			activity.detail.files = activity.detail.files.map((file) => ({ ...file, patch: file.patch ?? patches?.find((patch) => patch.path === file.path)?.patch }));
		}
	}
	return items;
}

/** Steering is active-turn/attempt-specific, never inferred from a queued send. */
export function cloudConversationCapabilities(session: Session, events: readonly ClientEvent[]): string[] {
	const active = session.activeTurn;
	if (session.interfaceMode !== "chat" || !session.runtimeConnected || session.isTerminated || active?.state !== "running") return [];
	const projected = toConversationTurns(events).find((turn) => turn.id === active.id);
	if (projected && ["completed", "failed", "interrupted"].includes(projected.state)) return [];
	const event = [...events].reverse().find((event) => event.type === "chat.turn_capabilities" && eventTurnId(event) === active.id && eventAttempt(event) === active.attemptCount);
	return event && payloadField(event, "steering") === true ? ["steer"] : [];
}

/**
 * Pages a cloud transcript to its end, since a single `replayEvents` call is
 * server-limited (`eventPageLimit`, currently 100) and reports `hasMore` when
 * there is more. A conversation snapshot must show the *live* tail, not
 * whatever the first page happened to contain, so this keeps requesting with
 * the returned `nextAfter` until the server says there is no more.
 *
 * Guarded against a page that claims `hasMore: true` without advancing the
 * cursor: that would otherwise re-request the same page forever, hanging the
 * app on a server bug rather than just returning what was fetched so far.
 */
export async function fetchConversationReplay(
	client: CloudClient,
	orgId: string,
	sessionId: string,
): Promise<{ events: ClientEvent[]; latestSequence: number }> {
	const events: ClientEvent[] = [];
	let cursor = 0;
	for (;;) {
		const page = await client.replayEvents(orgId, sessionId, { after: cursor });
		events.push(...page.events);
		if (!page.hasMore || page.nextAfter <= cursor) return { events, latestSequence: page.nextAfter };
		cursor = page.nextAfter;
	}
}

export type PollOptions = {
	client: CloudClient;
	orgId: string;
	sessionId: string;
	/** Cursor to resume from; only events after this sequence are requested. */
	after: number;
	onEvents(events: ClientEvent[], cursor: number): void;
	signal: AbortSignal;
	intervalMs?: number;
};

/**
 * Polls the cloud transcript from a cursor until aborted.
 *
 * Polling rather than SSE is deliberate: React Native has no EventSource and
 * expo/fetch streaming hits a JNI global-reference ceiling that aborts
 * long-lived responses. The control plane's chat-events replay is
 * cursor-based, so this loses nothing but latency, and mobile already polls
 * rather than streams on the tunnel path.
 *
 * A transient request failure must not kill the loop -- the phone's network
 * drops constantly, and the next tick simply retries from the same cursor.
 * An aborted signal stops the loop promptly and never leaves a pending timer
 * behind, so callers can `AbortController#abort()` and be done.
 */
export async function pollCloudEvents(options: PollOptions): Promise<void> {
	const interval = options.intervalMs ?? 2000;
	let cursor = options.after;
	while (!options.signal.aborted) {
		// Set only when a page reports more is waiting *and* the cursor actually
		// moved forward (a backlog page capped at the server's eventPageLimit).
		// Catching up must not idle for a full interval between pages -- at 100
		// events/page and a 2s interval, a 14k-event backlog would otherwise
		// trickle in for minutes before reaching the live tail. But hasMore
		// alone is not enough: if the server ever reports hasMore without the
		// cursor advancing, treating that as progress spins this loop against a
		// live server at round-trip rate, ignoring intervalMs entirely. A
		// non-advancing cursor is "caught up" for pacing purposes even though
		// the server claims otherwise.
		let advanced = false;
		const previousCursor = cursor;
		try {
			const page = await options.client.replayEvents(options.orgId, options.sessionId, {
				after: cursor,
				signal: options.signal,
			});
			// `nextAfter` is the highest sequence returned, or the supplied cursor
			// when the page is empty -- so this always advances monotonically and
			// never regresses on an empty page.
			cursor = page.nextAfter;
			advanced = page.hasMore && page.nextAfter > previousCursor;
			if (page.events.length > 0) {
				options.onEvents(page.events, cursor);
			}
		} catch {
			if (options.signal.aborted) return;
			// Transient failure: fall through to the wait below and retry.
		}
		if (options.signal.aborted) return;
		if (advanced) continue;
		await new Promise<void>((resolve) => {
			const timer = setTimeout(resolve, interval);
			options.signal.addEventListener(
				"abort",
				() => {
					clearTimeout(timer);
					resolve();
				},
				{ once: true },
			);
		});
	}
}
