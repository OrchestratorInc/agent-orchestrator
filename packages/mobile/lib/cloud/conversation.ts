import type {
	ActivityDetail,
	ActivityKind,
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationPlan,
	ConversationSnapshot,
	ConversationTurn,
	ConversationUsage,
	DecisionOption,
	FileChange,
	MessageFile,
	PlanStep,
} from "../chat/types";
import { cloudLifecycleStage } from "./lifecycle";
import type { CloudClientEvent, CloudSession } from "./types";

// Projects a cloud session's durable chat events onto the phone's conversation
// model, so the existing timeline draws it. The projection follows the
// desktop's (frontend/src/renderer/components/chat/CloudSessionChatSurface.tsx
// toSnapshot) so a turn reads the same on both.

type Payload = Record<string, unknown>;

function payloadOf(event: CloudClientEvent): Payload {
	return event.payload && typeof event.payload === "object" ? event.payload as Payload : {};
}

function str(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}

function turnIdOf(event: CloudClientEvent): string | undefined {
	return str(payloadOf(event).turnId);
}

/** Keep only events newer than what is already held; the cursor never moves backwards. */
export function appendCloudEvents(existing: readonly CloudClientEvent[], incoming: readonly CloudClientEvent[]): CloudClientEvent[] {
	const last = existing.at(-1)?.sequence ?? 0;
	const fresh = incoming.filter((event) => event.sequence > last).sort((a, b) => a.sequence - b.sequence);
	return fresh.length ? [...existing, ...fresh] : existing as CloudClientEvent[];
}

/**
 * Both lists' events in sequence order, each once. The poll and the live
 * stream fill the same list, so either may already hold events the other is
 * about to add; a union never drops one because the other got further ahead.
 */
export function mergeCloudEvents(existing: readonly CloudClientEvent[], incoming: readonly CloudClientEvent[]): CloudClientEvent[] {
	const last = existing.at(-1)?.sequence ?? 0;
	if (incoming.every((event, index) => event.sequence > last && (index === 0 || event.sequence > incoming[index - 1].sequence))) {
		return incoming.length ? [...existing, ...incoming] : existing as CloudClientEvent[];
	}
	const bySequence = new Map<number, CloudClientEvent>();
	for (const event of existing) bySequence.set(event.sequence, event);
	let added = false;
	for (const event of incoming) {
		if (bySequence.has(event.sequence)) continue;
		bySequence.set(event.sequence, event);
		added = true;
	}
	return added ? [...bySequence.values()].sort((a, b) => a.sequence - b.sequence) : existing as CloudClientEvent[];
}

/** Split a unified git diff into per-file changes with counts. */
export function cloudTurnDiff(patch: string): FileChange[] {
	const files: FileChange[] = [];
	let current: (FileChange & { patch: string }) | undefined;
	let inHunk = false;
	for (const line of patch.replace(/\n$/, "").split("\n")) {
		if (line.startsWith("diff --git ")) {
			if (current) files.push(current);
			const path = line.match(/^diff --git a\/.+ b\/(.+)$/)?.[1];
			current = path ? { path, status: "modified", additions: 0, deletions: 0, patch: `${line}\n` } : undefined;
			inHunk = false;
		} else if (current) {
			current.patch += `${line}\n`;
			if (line.startsWith("new file mode")) current.status = "added";
			else if (line.startsWith("deleted file mode")) current.status = "deleted";
			else if (line.startsWith("rename from ")) { current.status = "renamed"; current.oldPath = line.slice(12); }
			else if (line.startsWith("rename to ")) { current.status = "renamed"; current.path = line.slice(10); }
			else if (line.startsWith("+++ b/")) current.path = line.slice(6);
			else if (line.startsWith("@@")) inHunk = true;
			else if (inHunk && line.startsWith("+")) current.additions++;
			else if (inHunk && line.startsWith("-")) current.deletions++;
		}
	}
	if (current) files.push(current);
	return files;
}

const ACTIVITY_KINDS = new Set<ActivityKind>(["command", "file_change", "reasoning", "plan", "mcp_tool"]);
const ENDED = new Set(["chat.turn_completed", "chat.turn_interrupted", "chat.turn_aborted"]);
// Provider payloads are not renderer types: drop display fields that are not strings.
const STRING_FIELDS = ["text", "command", "rawCommand", "cwd", "reason", "providerToolName", "providerTitle", "server", "toolName", "namespace", "error", "progress", "explanation", "message", "url"];

function safeDetail(raw: Payload): ActivityDetail {
	const detail: ActivityDetail = { ...raw };
	for (const key of STRING_FIELDS) if (detail[key] !== undefined && typeof detail[key] !== "string") delete detail[key];
	if (Array.isArray(raw.steps)) {
		detail.steps = raw.steps.filter((step): step is { text: string; status: "pending" | "in_progress" | "completed" } =>
			!!step && typeof step === "object" && typeof (step as Payload).text === "string" && ["pending", "in_progress", "completed"].includes((step as Payload).status as string));
	} else delete detail.steps;
	if (Array.isArray(raw.files)) {
		detail.files = raw.files.flatMap((file): FileChange[] => {
			if (!file || typeof file !== "object" || typeof (file as Payload).path !== "string") return [];
			const value = file as Payload;
			const status = ["added", "modified", "deleted", "renamed"].includes(value.status as string) ? value.status as FileChange["status"] : "modified";
			return [{
				path: value.path as string, status,
				additions: typeof value.additions === "number" && Number.isFinite(value.additions) ? value.additions : 0,
				deletions: typeof value.deletions === "number" && Number.isFinite(value.deletions) ? value.deletions : 0,
				patch: typeof value.patch === "string" ? value.patch : undefined,
			}];
		});
	} else delete detail.files;
	return detail;
}

/** A worker's turn_plan activity as the turn's plan (the worker re-sends the whole plan as it changes). */
export function cloudPlan(detail: Payload): ConversationPlan | undefined {
	const raw = detail.plan;
	if (!raw || typeof raw !== "object") return undefined;
	const plan = raw as Payload;
	if (!Array.isArray(plan.steps)) return undefined;
	const steps = plan.steps.flatMap((value): PlanStep[] => {
		if (!value || typeof value !== "object") return [];
		const step = value as Payload;
		if (typeof step.text !== "string") return [];
		return [{ text: step.text, status: step.status === "in_progress" || step.status === "completed" ? step.status : "pending" }];
	});
	return {
		steps,
		...(typeof plan.explanation === "string" && plan.explanation.trim() ? { explanation: plan.explanation } : {}),
		...(plan.spansTurns === true ? { spansTurns: true } : {}),
	};
}

/** A worker's usage activity as the conversation's context use, or undefined when malformed. */
export function cloudUsage(detail: Payload): ConversationUsage | undefined {
	const raw = detail.usage;
	if (!raw || typeof raw !== "object") return undefined;
	const usage = raw as Payload;
	if (typeof usage.contextUsed !== "number") return undefined;
	const count = (value: unknown) => (typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0);
	return {
		contextUsed: count(usage.contextUsed),
		contextWindow: count(usage.contextWindow),
		inputTokens: count(usage.inputTokens),
		outputTokens: count(usage.outputTokens),
		cachedTokens: count(usage.cachedTokens),
		totalTokens: count(usage.totalTokens),
		...(typeof usage.cost === "number" ? { cost: usage.cost } : {}),
		...(typeof usage.currency === "string" ? { currency: usage.currency } : {}),
	};
}

/** The files a chat.user_message event lists. */
function messageFiles(listed: unknown): MessageFile[] {
	if (!Array.isArray(listed)) return [];
	return listed.flatMap((item): MessageFile[] => {
		if (!item || typeof item !== "object") return [];
		const { id, fileName, contentType, size } = item as Payload;
		return typeof id === "string" && typeof fileName === "string" && typeof contentType === "string" && typeof size === "number"
			? [{ id, name: fileName, mimeType: contentType, size }] : [];
	});
}

/** The quotes a person's message recorded (from the desktop): its transcript shows them beside what was typed. */
function messageQuotes(listed: unknown): ConversationMessage["content"] {
	if (!Array.isArray(listed)) return undefined;
	const quotes = listed.flatMap((value) => {
		if (!value || typeof value !== "object") return [];
		const block = value as Payload;
		return block.type === "excerpt" && typeof block.text === "string"
			? [{ type: "excerpt", text: block.text, sourceMessageId: str(block.sourceMessageId), sourceRevision: typeof block.sourceRevision === "number" ? block.sourceRevision : undefined }]
			: [];
	});
	return quotes.length ? quotes : undefined;
}

/** What an approval asks to do, when the worker names it: wording differs for a file change and a command. */
const APPROVAL_SUBJECT_KINDS: readonly ActivityKind[] = ["command", "file_change", "mcp_tool"];

type ScriptRun = { id: string; phase: string; status: "running" | "completed" | "failed"; steps: number; total: number; command?: string; output?: string; firstSequence: number; lastSequence: number; createdAt: string };

function scriptRunActivity(run: ScriptRun): ConversationActivity {
	const phase = run.phase === "cleanup" ? "Cleanup" : "Setup";
	const summary = run.status === "running"
		? `${phase} running (${Math.min(run.steps, run.total)}/${run.total})`
		: run.status === "completed" ? `${phase} passed` : `${phase} failed`;
	return {
		kind: "activity", id: `cloud-script-run-${run.id}`, sequence: run.firstSequence, revision: run.lastSequence,
		activityKind: "command", status: run.status, summary,
		detail: { command: run.command, output: run.output, event: "workspace.script.run" },
		createdAt: run.createdAt,
	};
}

export type CloudSnapshotOptions = {
	/** The viewer's own model and settings for the composer (stored per session on the phone). */
	settings?: ConversationSnapshot["settings"];
	/**
	 * The events are the newest part of the history (older pages load on
	 * demand), so the first of them is not the session's first.
	 */
	partial?: boolean;
};

/**
 * The conversation a cloud session's events describe.
 *
 * Turns come from the turn events; assistant text is concatenated per
 * turn and item; activities are merged by id; approvals settle on their
 * decision or when their turn ends. The first queued turn is shown as running
 * when nothing else is, because the worker may take a moment to claim it.
 */
export function cloudConversationSnapshot(session: CloudSession, events: readonly CloudClientEvent[], options: CloudSnapshotOptions = {}): ConversationSnapshot {
	const turns = new Map<string, ConversationTurn>();
	const assistant = new Map<string, ConversationMessage>();
	const approvals = new Map<string, ConversationActivity>();
	const activities = new Map<string, ConversationActivity>();
	const patches = new Map<string, FileChange[]>();
	const steerable = new Set<string>();
	const runs = new Map<string, { run: ScriptRun; activity: ConversationActivity }>();
	const items: ConversationItem[] = [];
	let usage: ConversationUsage | undefined;
	const partial = options.partial === true;
	// A turn whose prompt is above the loaded events began before every prompt
	// here (a prompt queued while it ran sits inside it, ahead of its later
	// events): it sorts before them, in the order of its first loaded event.
	const windowStart = events[0]?.sequence ?? 0;
	const windowSpan = (events.at(-1)?.sequence ?? 0) - windowStart + 2;

	for (const event of events) {
		const payload = payloadOf(event);
		const turnId = turnIdOf(event);
		if (turnId && !turns.has(turnId)) {
			// A turn starts with its prompt. One whose first event here is any other
			// began before the loaded events (a page starts inside the turn a later
			// prompt queued in, or inside a very long one): it had started.
			const startedEarlier = partial && event.type !== "chat.user_message" && event.type !== "chat.turn_started";
			const promptAbove = partial && event.type !== "chat.user_message";
			turns.set(turnId, {
				...(startedEarlier
					? { id: turnId, state: "running", requestedAt: event.createdAt, startedAt: event.createdAt }
					: { id: turnId, state: "queued", requestedAt: event.createdAt }),
				...(promptAbove ? { anchorSequence: windowStart - 1 + (event.sequence - windowStart + 1) / windowSpan } : {}),
			});
		}
		const turn = turnId ? turns.get(turnId) : undefined;

		if (turn && event.type === "chat.turn_started") {
			turn.state = "running";
			turn.startedAt = event.createdAt;
			turn.completedAt = undefined;
			turn.errorMessage = undefined;
			continue;
		}
		if (turn && event.type === "chat.turn_capabilities") {
			if (payload.steering === true) steerable.add(turn.id);
			continue;
		}
		if (turn && ENDED.has(event.type)) {
			turn.state = event.type === "chat.turn_completed" ? "completed" : event.type === "chat.turn_interrupted" ? "interrupted" : "failed";
			turn.completedAt = event.createdAt;
			turn.errorMessage = str(payload.error);
			continue;
		}
		if (event.type === "chat.approval_requested") {
			const requestId = str(payload.requestId);
			if (!requestId) continue;
			const decisions: DecisionOption[] = Array.isArray(payload.decisions)
				? payload.decisions.flatMap((value): DecisionOption[] => {
					const decision = value as Payload | null;
					return decision && typeof decision.id === "string" && typeof decision.label === "string" ? [{ id: decision.id, label: decision.label }] : [];
				})
				: [];
			const activity: ConversationActivity = {
				kind: "activity", id: `cloud-approval-${requestId}`, turnId, sequence: event.sequence, revision: 1,
				activityKind: "approval", status: "pending", summary: str(payload.summary) ?? "Permission required",
				requestId, decisions, detail: {
					toolKind: str(payload.toolKind),
					subjectKind: APPROVAL_SUBJECT_KINDS.find((kind) => kind === payload.subjectKind),
					decisions,
				},
				createdAt: event.createdAt,
			};
			approvals.set(requestId, activity);
			items.push(activity);
			continue;
		}
		if (event.type === "chat.approval_decided") {
			const activity = approvals.get(str(payload.requestId) ?? "");
			if (activity) {
				activity.status = "resolved";
				activity.revision++;
				activity.detail = { ...activity.detail, decision: str(payload.decision) };
			}
			continue;
		}
		if (turn && event.type === "chat.activity") {
			const raw = payload.activity;
			if (!raw || typeof raw !== "object") continue;
			const value = raw as Payload;
			const id = str(value.id);
			const kind = str(value.kind);
			if (!id || !kind) continue;
			const rawDetail = value.detail && typeof value.detail === "object" ? value.detail as Payload : {};
			if (kind === "turn_plan") {
				const plan = cloudPlan(rawDetail);
				if (plan) turn.plan = plan;
				continue;
			}
			if (kind === "usage") {
				usage = cloudUsage(rawDetail) ?? usage;
				continue;
			}
			if (kind === "turn_diff") {
				if (typeof rawDetail.diff === "string") {
					const files = cloudTurnDiff(rawDetail.diff);
					turn.diff = { files, truncated: rawDetail.truncated === true };
					patches.set(turn.id, files);
				}
				continue;
			}
			if (kind === "plan") {
				const detail = safeDetail(rawDetail);
				if (detail.steps?.length) turn.plan = { explanation: detail.explanation, steps: detail.steps };
			}
			const key = `${turn.id}:${id}`;
			if (kind === "system") {
				// The worker's turn notice (an approval mode it substituted). Only its words are kept.
				if (rawDetail.event !== "approval_mode.substituted" || typeof value.summary !== "string" || activities.has(key)) continue;
				const notice: ConversationActivity = {
					kind: "activity", id: `cloud-activity-${key}`, turnId: turn.id, sequence: event.sequence, revision: 1,
					activityKind: "system", status: "completed", summary: value.summary, detail: { event: "approval_mode.substituted" }, createdAt: event.createdAt,
				};
				activities.set(key, notice);
				items.push(notice);
				continue;
			}
			if (!ACTIVITY_KINDS.has(kind as ActivityKind)) continue;
			const status = value.status === "completed" || value.status === "failed" ? value.status : "running";
			const detail = safeDetail(rawDetail);
			const previous = activities.get(key);
			if (previous) {
				previous.status = status;
				if (typeof value.summary === "string") previous.summary = value.summary;
				const merged: ActivityDetail = { ...previous.detail, ...detail };
				if (typeof rawDetail.textDelta === "string") merged.text = `${previous.detail?.text ?? ""}${rawDetail.textDelta}`;
				if (typeof rawDetail.outputDelta === "string") {
					merged.output = `${typeof previous.detail?.output === "string" ? previous.detail.output : ""}${rawDetail.outputDelta}`;
					merged.outputSource = "stream";
					merged.outputMayBePartial = true;
				}
				previous.detail = merged;
				previous.revision++;
			} else {
				const activity: ConversationActivity = {
					kind: "activity", id: `cloud-activity-${key}`, turnId: turn.id, sequence: event.sequence, revision: 1,
					// The provider's own item id, which a subagent's steps name as their parent.
					providerItemId: id,
					activityKind: kind as ActivityKind, status, summary: str(value.summary) ?? kind,
					detail: {
						...detail,
						text: typeof rawDetail.textDelta === "string" ? rawDetail.textDelta : detail.text,
						output: typeof rawDetail.outputDelta === "string" ? rawDetail.outputDelta : typeof rawDetail.output === "string" ? rawDetail.output : undefined,
					},
					createdAt: event.createdAt,
				};
				activities.set(key, activity);
				items.push(activity);
			}
			continue;
		}
		if (event.type === "workspace.script.step") {
			const runId = str(payload.runId);
			const step = typeof payload.step === "number" ? payload.step : undefined;
			if (!runId || !step) continue;
			const total = Math.max(typeof payload.total === "number" ? payload.total : step, step);
			const rawStatus = str(payload.status);
			const failed = rawStatus === "failed" || rawStatus === "timed_out" || rawStatus === "interrupted";
			const entry = runs.get(runId);
			const run: ScriptRun = entry?.run ?? {
				id: runId, phase: str(payload.phase) ?? "setup", status: "running", steps: 0, total, firstSequence: event.sequence, lastSequence: event.sequence, createdAt: event.createdAt,
			};
			run.steps = Math.max(run.steps, step);
			run.total = Math.max(run.total, total);
			run.lastSequence = event.sequence;
			run.command = str(payload.command) ?? run.command;
			if (typeof payload.output === "string" && payload.output.trim()) run.output = payload.output;
			run.status = failed ? "failed" : rawStatus === "succeeded" && step >= run.total ? "completed" : run.status === "failed" ? "failed" : "running";
			if (entry) Object.assign(entry.activity, scriptRunActivity(run));
			else {
				const activity = scriptRunActivity(run);
				runs.set(runId, { run, activity });
				items.push(activity);
			}
			continue;
		}
		if (event.type === "workspace.setup") {
			// Workers that send step events summarize the same run again; keep only one row.
			if (runs.size > 0) continue;
			const status = payload.status === "succeeded" ? "completed" : "failed";
			items.push({
				kind: "activity", id: `cloud-project-setup-${event.sequence}`, sequence: event.sequence, revision: 1,
				activityKind: "system", status, summary: status === "completed" ? "Setup passed" : payload.status === "timed_out" ? "Setup timed out" : "Setup failed",
				detail: { event: "workspace.setup", output: str(payload.output) }, createdAt: event.createdAt,
			});
			continue;
		}

		const text = str(payload.text);
		if (event.type === "chat.user_message") {
			// A message of only files has no text.
			const files = messageFiles(payload.attachments);
			if (!text && !files.length) continue;
			const automation = payload.origin === "automation";
			// A desktop message with quotes reads as it was typed, the quotes beside
			// it; the agent got them inlined in `text`.
			const quotes = automation ? undefined : messageQuotes(payload.content);
			items.push({
				kind: "message", id: `cloud-event-${event.sequence}`, sequence: event.sequence, revision: 1, turnId,
				role: "user", origin: automation ? "automation" : "human",
				text: automation ? str(payload.displayText) ?? text ?? "" : quotes ? str(payload.displayText) ?? "" : text ?? "",
				...(quotes ? { content: quotes } : {}),
				...(files.length ? { files } : {}),
				senderLabel: automation ? str(payload.senderLabel) : undefined,
				senderSessionId: automation ? str(payload.senderSessionId) : undefined,
				senderProjectId: automation ? str(payload.senderProjectId) : undefined,
				senderDisplayName: automation ? str(payload.senderDisplayName) : undefined,
				streaming: false, delivery: "accepted", createdAt: event.createdAt,
			});
			continue;
		}
		if (!text) continue;
		if (event.type === "chat.turn_steered") {
			items.push({
				kind: "activity", id: `cloud-steer-${event.sequence}`, turnId, sequence: event.sequence, revision: 1,
				activityKind: "system", status: "completed", summary: `Steered: ${text}`,
				detail: { event: "steer", text, origin: "human", clientMessageId: str(payload.clientMessageId) }, createdAt: event.createdAt,
			});
			continue;
		}
		if (event.type !== "chat.assistant_delta") continue;
		// Older Codex workers persisted this CLI status line as assistant text.
		if (session.harness === "codex" && text.trim() === "Reading additional input from stdin...") continue;
		const key = `${turnId ?? `event-${event.sequence}`}:${str(payload.itemId) ?? "reply"}`;
		const previous = assistant.get(key);
		if (previous) {
			previous.text += text;
			previous.revision++;
			continue;
		}
		const message: ConversationMessage = {
			kind: "message", id: `cloud-assistant-${key}`, turnId, sequence: event.sequence, revision: 1,
			role: "assistant", origin: "provider", text, streaming: true, createdAt: event.createdAt,
		};
		assistant.set(key, message);
		items.push(message);
	}

	// A file change gets its patch from the turn's diff when the worker sent one.
	for (const activity of activities.values()) {
		if (activity.activityKind !== "file_change" || !activity.turnId || !Array.isArray(activity.detail?.files)) continue;
		const turnPatches = patches.get(activity.turnId) ?? [];
		activity.detail.files = (activity.detail.files as Array<FileChange | string>).map((file) => {
			if (typeof file === "string") return file;
			const match = turnPatches.find((candidate) => candidate.path === file.path);
			return match ? { ...file, patch: file.patch ?? match.patch } : file;
		}) as FileChange[];
	}
	const ordered = [...turns.values()];
	const stage = cloudLifecycleStage(session);
	// A paused or starting sandbox runs nothing yet: its queued turn waits, it is not working.
	const live = !stage || stage === "connected";
	// The first durable send may wait for a worker to claim it; it is the active request.
	if (live && !ordered.some((turn) => turn.state === "running") && !session.isTerminated) {
		const next = ordered.find((turn) => turn.state === "queued");
		if (next) next.state = "running";
	}
	const running = new Set(ordered.filter((turn) => turn.state === "running").map((turn) => turn.id));
	for (const activity of activities.values()) {
		if (activity.status === "running" && activity.turnId && !running.has(activity.turnId)) { activity.status = "cancelled"; activity.revision++; }
	}
	for (const message of assistant.values()) if (!message.turnId || !running.has(message.turnId)) message.streaming = false;
	for (const approval of approvals.values()) {
		if (approval.status === "pending" && approval.turnId && !running.has(approval.turnId) && turns.get(approval.turnId)?.state !== "queued") {
			approval.status = "cancelled";
			approval.revision++;
		}
	}
	const active = ordered.find((turn) => turn.state === "running");
	return {
		conversationId: `cloud:${session.id}`,
		sessionId: session.id,
		harness: session.harness,
		mode: "chat",
		controller: {
			state: session.isTerminated ? "stopped"
				: active ? "busy"
					: stage === "paused" ? "hibernated"
						: !live ? "connecting"
							: "ready",
		},
		turns: ordered,
		items,
		latestSequence: events.at(-1)?.sequence ?? 0,
		oldestSequence: events[0]?.sequence ?? 1,
		hasMoreBefore: partial,
		settings: options.settings ?? {},
		...(usage ? { usage } : {}),
		capabilities: active && steerable.has(active.id) ? ["steer"] : [],
	};
}

/** The running turn a Stop applies to. */
export function runningCloudTurnId(snapshot: ConversationSnapshot): string | undefined {
	return snapshot.turns.find((turn) => turn.state === "running")?.id;
}
