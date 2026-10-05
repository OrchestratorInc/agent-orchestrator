import type { CloudClient, Turn } from "@aoagents/cloud-client";
import type { DashboardSession, ProjectInfo } from "../api";
import type { ConversationEvent } from "../chat/sse";
import type { ConversationTurn, TurnSettings } from "../chat/types";
import type { SessionSource } from "../environment/types";
import { fetchConversationReplay, pollCloudEvents, toConversationItems, toConversationTurns } from "./events";
import { toDashboardSession, toProjectInfo } from "./mapping";

/** How often the cloud transcript is polled for new events. Matches the
 * tunnel-path conversation poll interval mobile already uses elsewhere. */
const CLOUD_EVENT_POLL_MS = 2_000;
const CHAT_HANDOFF_POLL_MS = 1_000;
const CHAT_HANDOFF_TIMEOUT_MS = 60_000;
const terminalTransitionPhases = new Set(["completed", "failed", "cancelled"]);
const reasoningEfforts = new Set(["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]);

async function waitForChatHandoff(client: CloudClient, orgId: string, id: string): Promise<void> {
	const deadline = Date.now() + CHAT_HANDOFF_TIMEOUT_MS;
	for (;;) {
		const { session } = await client.getSession(orgId, id);
		if (session.interfaceMode === "chat") return;
		const status = await client.getSessionInterfaceTransition(orgId, id);
		const transition = status.transition;
		if (transition?.phase === "failed" || transition?.phase === "cancelled" || transition?.phase === "recovery_required") {
			throw new Error(transition.errorDetail || `Cloud could not switch this session to Chat (${transition.phase}).`);
		}
		if (Date.now() >= deadline) throw new Error("Cloud is taking too long to switch this session to Chat. Try again shortly.");
		await new Promise<void>((resolve) => setTimeout(resolve, CHAT_HANDOFF_POLL_MS));
	}
}

function toConversationTurn(turn: Turn): ConversationTurn {
	const state = turn.state === "provisioning"
		? "queued"
		: turn.state === "cancel_requested"
			? "running"
			: turn.state;
	return {
		id: turn.id,
		state,
		errorMessage: turn.errorMessage,
		requestedAt: turn.createdAt,
		startedAt: turn.startedAt,
		completedAt: turn.completedAt,
	};
}

/** Page through a cursor-paginated cloud list until it ends. */
async function collect<T>(
	fetchPage: (cursor?: string) => Promise<{ items: T[]; page: { hasMore?: boolean; nextCursor?: string } }>,
): Promise<T[]> {
	const all: T[] = [];
	let cursor: string | undefined;
	for (;;) {
		const page = await fetchPage(cursor);
		all.push(...page.items);
		if (page.page?.hasMore !== true || !page.page.nextCursor) return all;
		cursor = page.page.nextCursor;
	}
}

export function createCloudSessionSource(input: {
	client: CloudClient;
	orgId: string;
}): SessionSource {
	const { client, orgId } = input;
	const settingsBySession = new Map<string, TurnSettings>();
	return {
		kind: "cloud",
		listProjects: async (): Promise<ProjectInfo[]> =>
			(await collect((cursor) => client.listProjects(orgId, { cursor }))).map(toProjectInfo),
		listSessions: async (): Promise<DashboardSession[]> => {
			const sessions = await collect((cursor) => client.listSessions(orgId, { cursor }));
			return sessions.map(toDashboardSession);
		},
		createSession: async (options) => {
			if (!options.projectId) throw new Error("Pick a project first");
			const response = await client.createSession(
				orgId,
				{
					projectId: options.projectId,
					kind: "worker",
					harness: options.harness ?? "claude-code",
					displayName: (options.prompt ?? "New session").slice(0, 80),
					prompt: options.prompt ?? "",
					// Cloud's `mode` is a trust level, distinct from mobile's chat/tui
					// controller mode; a spawned session starts fully trusted, matching
					// desktop's default for worker sessions.
					mode: "trusted",
					deniedCommands: [],
				},
				// Idempotent: a retry on a flaky phone network must not spawn twice.
				{ idempotencyKey: `spawn-${Date.now()}-${Math.random().toString(36).slice(2)}` },
			);
			return { id: response.session.id };
		},
		deleteSession: async (id) => { await client.deleteSession(orgId, id); },
		getConversationPage: async (id) => {
			const [{ session }, { events, latestSequence }] = await Promise.all([
				client.getSession(orgId, id),
				// A single replayEvents call is capped at the server's page limit
				// and reports hasMore; paging to the end here is required so this
				// shows the live tail of a long conversation, not just its start.
				fetchConversationReplay(client, orgId, id),
			]);
			const turns = toConversationTurns(events);
			if (session.activeTurn && !turns.some((turn) => turn.id === session.activeTurn?.id)) {
				turns.push(toConversationTurn(session.activeTurn));
			}
			return {
				conversationId: id,
				sessionId: id,
				harness: session.harness,
				// Cloud's `mode` is a trust level; interfaceMode is the actual
				// controller. A Terminal session must not masquerade as Chat.
				mode: session.interfaceMode === "tui" ? "tui" as const : "chat" as const,
				controller: { state: turns.some((turn) => turn.state === "running" || turn.state === "queued") ? "busy" as const : "ready" as const },
				latestSequence,
				oldestSequence: events.length > 0 ? events[0].sequence : latestSequence,
				// No backward pagination yet: a full forward replay is the whole
				// transcript the control plane will hand back today.
				hasMoreBefore: false,
				turns,
				items: toConversationItems(events),
				settings: settingsBySession.get(id) ?? {},
			};
		},
		sendMessage: async (id, input) => {
			const settings = settingsBySession.get(id);
			const message = settings && (settings.model || settings.reasoningEffort || settings.approvalMode)
				? {
					text: input.text,
					...(settings.model ? { model: settings.model } : {}),
					...(settings.reasoningEffort && reasoningEfforts.has(settings.reasoningEffort)
						? { reasoningEffort: settings.reasoningEffort as "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" } : {}),
					...(settings.approvalMode ? { approvalMode: settings.approvalMode } : {}),
				} : input.text;
			// The composer's clientMessageId is already a per-message unique id,
			// so it doubles as the idempotency key: a retry after a dropped
			// response cannot post the message twice.
			await client.sendMessage(orgId, id, message, { idempotencyKey: input.clientMessageId });
			// The wire response is `{ event: UserMessageEvent }`, not a turn — the
			// control plane does not hand back a turn id for the message just
			// posted. Reporting a turnId here would be a guess the UI could
			// mistake for a real turn (e.g. treating it as cancellable), so it is
			// left undefined; nothing in mobile reads the immediate sendMessage
			// return value today (useConversation's `deliver` discards it and
			// relies on the polled transcript instead), so this is safe.
			return { duplicate: false };
		},
		// Returns 202 with a body (ResumeSessionResponse), not 204 — the
		// reconciler owns the actual provider/worker transition from here, so
		// the response body is discarded and the caller relies on the polled
		// lifecycle stage (cloudLifecycleStage) to see it land.
		resumeSession: async (id) => {
			await client.resumeSession(orgId, id);
		},
		switchToChat: async (id) => {
			const { session } = await client.getSession(orgId, id);
			if (session.interfaceMode === "chat") return;
			const status = await client.getSessionInterfaceTransition(orgId, id);
			if (!status.supported) throw new Error(status.reason || "Cloud Chat is unavailable for this session.");
			const active = status.transition && !terminalTransitionPhases.has(status.transition.phase);
			if (active && status.transition?.targetMode !== "chat") {
				throw new Error("This session is already switching to Terminal. Wait for that change to finish.");
			}
			if (!active) await client.startSessionInterfaceTransition(orgId, id, { targetMode: "chat", policy: "drain" });
			await waitForChatHandoff(client, orgId, id);
		},
		decideApproval: async (id, requestId, decisionId) => {
			await client.decideChatApproval(orgId, id, requestId, decisionId);
		},
		getChatModels: async (id) => {
			const { session } = await client.getSession(orgId, id);
			return session.harness === "codex" ? (await client.listChatModels(orgId, id)).models : [];
		},
		setTurnSettings: async (id, settings) => {
			settingsBySession.set(id, { ...settings });
		},
		cancelTurn: async (id, turnId) => {
			// Keyed by the turn id, not freshly generated per call, so a retried
			// cancel of the same turn is idempotent instead of firing a second
			// distinct request.
			await client.cancelTurn(orgId, id, turnId, { idempotencyKey: `cancel-${turnId}` });
		},
		subscribeEvents: (id, listener) => {
			const controller = new AbortController();
			// KNOWN GAP — do not wire this into useConversation until it's fixed.
			// `after: 0` is hardcoded with no cursor persisted across calls, so a
			// fresh subscribeEvents() (e.g. on remount or a reconnect) replays the
			// *entire* transcript as "new" events. Unlike the local path, which
			// persists a poll cursor (see lib/chat/conversationPoll.ts), there is
			// no equivalent here. The real fix needs a SessionSource redesign to
			// carry a resumable cursor and is out of scope for this pass — see
			// item 7 of .superpowers/sdd/2026-09-19-mobile-cloud-environments/
			// final-fix-report.md.
			void pollCloudEvents({
				client,
				orgId,
				sessionId: id,
				after: 0,
				signal: controller.signal,
				intervalMs: CLOUD_EVENT_POLL_MS,
				onEvents: (events) => {
					for (const event of events) {
						const conversationEvent: ConversationEvent = {
							seq: event.sequence,
							// Cloud has no project-scoped stream the way the daemon's global
							// CDC feed does; nothing downstream reads this field yet.
							projectId: "",
							sessionId: event.sessionId,
							type: event.type,
							payload: event.payload,
							createdAt: event.createdAt,
						};
						listener(conversationEvent);
					}
				},
			});
			return () => controller.abort();
		},
	};
}
