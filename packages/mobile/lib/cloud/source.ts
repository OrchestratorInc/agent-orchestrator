import { CloudApiError, type CloudClient, type CreateSessionInput, type Turn } from "@aoagents/cloud-client";
import type { DashboardSession, ProjectInfo, SpawnOptions } from "../api";
import type { ConversationEvent } from "../chat/sse";
import type { ConversationTurn, TurnSettings } from "../chat/types";
import type { SessionSource } from "../environment/types";
import { fetchConversationReplay, pollCloudEvents, toConversationItems, toConversationTurns } from "./events";
import { toDashboardSession, toProjectInfo } from "./mapping";
import { cloudRoleDefaults } from "../spawnModel";
import { acceptCloudOperation, prepareCloudOperation, readCloudOperation, reserveCloudOperation } from "./operation";
import { cloudHeaderControllerState, cloudLifecycleStage, cloudStartupProblem } from "./lifecycle";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { afterDraftWrites } from "../chat/pendingSend";
import { cloudConversationCapabilities } from "./events";
import { awaitCloudReplay, cloudConversationReplayCache, type CloudReplayEntry } from "./conversationReplay";

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
	scopeKey: string;
	isCurrent: () => boolean;
}): SessionSource {
	const { client, orgId, scopeKey, isCurrent } = input;
	const replays = new Map<string, { controller: AbortController; promise: Promise<CloudReplayEntry>; consumers: number }>();
	const subscriptions = new Map<string, number>();
	const requireCurrent = () => { if (!isCurrent()) throw new Error("Cloud account changed. Try again."); };
	const replay = async (id: string, signal?: AbortSignal): Promise<CloudReplayEntry> => {
		requireCurrent();
		let request = replays.get(id);
		if (request?.controller.signal.aborted) { replays.delete(id); request = undefined; }
		if (!request) {
			const controller = new AbortController();
			const unwatch = cloudConversationReplayCache.watchScope(scopeKey, () => controller.abort());
			const after = cloudConversationReplayCache.get(scopeKey, id)?.cursor ?? 0;
			const created = { controller, consumers: 0, promise: undefined as unknown as Promise<CloudReplayEntry> };
			created.promise = fetchConversationReplay(client, orgId, id, { after, signal: controller.signal }).then(({ events, latestSequence }) => {
				requireCurrent(); controller.signal.throwIfAborted();
				return cloudConversationReplayCache.apply(scopeKey, id, events, latestSequence);
			}).finally(() => { unwatch(); if (replays.get(id) === created) replays.delete(id); });
			request = created; replays.set(id, created);
		}
		request.consumers++;
		try { return await awaitCloudReplay(request.promise, signal); }
		finally { if (--request.consumers === 0) request.controller.abort(); }
	};
	const settingsBySession = new Map<string, TurnSettings>();
	const settingsKey = (id: string) => "ao.cloud.turn-settings.v1." + encodeURIComponent(JSON.stringify([scopeKey, id]));
	const normalizeSettings = (value: TurnSettings): TurnSettings => ({
		...(typeof value.model === "string" && value.model.trim() ? { model: value.model.trim() } : {}),
		...(typeof value.reasoningEffort === "string" && reasoningEfforts.has(value.reasoningEffort) ? { reasoningEffort: value.reasoningEffort } : {}),
		...(value.approvalMode && ["default", "accept-edits", "auto", "bypass-permissions"].includes(value.approvalMode) ? { approvalMode: value.approvalMode } : {}),
	});
	const readSettings = async (id: string): Promise<TurnSettings> => {
		if (settingsBySession.has(id)) return settingsBySession.get(id)!;
		let value: TurnSettings = {};
		await afterDraftWrites(settingsKey(id), async () => {
			const raw = await AsyncStorage.getItem(settingsKey(id));
			if (raw) value = normalizeSettings(JSON.parse(raw));
		});
		if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
		settingsBySession.set(id, value); return value;
	};
	const pendingSpawns = new Map<string, Promise<{ id: string }>>();
	const acceptedSpawns = new Map<string, string>();
	const createWorker = async (options: SpawnOptions): Promise<{ id: string }> => {
		if (!options.projectId) throw new Error("Pick a project first");
		if (!options.clientRequestId) throw new Error("This Cloud task has no request identity. Reopen the task sheet.");
		if (!isCurrent()) throw new Error("Cloud account changed. Reopen the task sheet.");
		const slot = `worker-create/${options.projectId}`;
		const existing = await readCloudOperation<CreateSessionInput>(scopeKey, slot);
		if (existing && existing.key !== options.clientRequestId) throw new Error("Resolve the previous Cloud request before starting a changed request.");
		const acceptedId = existing?.acceptedId ?? acceptedSpawns.get(options.clientRequestId);
		if (acceptedId) return { id: acceptedId };
		let operation = existing;
		if (!operation) {
			const { project } = await client.getProject(orgId, options.projectId);
			const harness = options.harness ?? "claude-code";
			const defaults = cloudRoleDefaults(project.config, "worker", harness);
			operation = prepareCloudOperation(undefined, scopeKey, {
				projectId: options.projectId, kind: "worker", harness,
				displayName: (options.prompt ?? "New session").slice(0, 80), prompt: options.prompt ?? "",
				mode: "trusted", deniedCommands: [], model: options.model ?? defaults.model,
				reasoningEffort: options.reasoningEffort ?? defaults.reasoningEffort,
			} satisfies CreateSessionInput, () => options.clientRequestId!);
		}
		if (!isCurrent()) throw new Error("Cloud account changed. Reopen the task sheet.");
		await reserveCloudOperation(slot, operation);
		if (!isCurrent()) throw new Error("Cloud account changed. Reopen the task sheet.");
		const { session } = await client.createSession(orgId, operation.payload, { idempotencyKey: operation.key });
		// Acceptance precedes board refresh. Storage failure cannot undo a server
		// acceptance; its reserved key still makes a future retry safe.
		acceptedSpawns.set(operation.key, session.id);
		try { await acceptCloudOperation(scopeKey, slot, operation.key, session.id); } catch {}
		return { id: session.id };
	};
	return {
		kind: "cloud",
		scopeKey,
		listProjects: async (): Promise<ProjectInfo[]> =>
			(await collect((cursor) => client.listProjects(orgId, { cursor }))).map(toProjectInfo),
		listSessions: async (): Promise<DashboardSession[]> => {
			const sessions = await collect((cursor) => client.listSessions(orgId, { cursor }));
			return sessions.map(toDashboardSession);
		},
		createSession: (options) => {
			const key = JSON.stringify([options.projectId, options.clientRequestId]);
			const pending = pendingSpawns.get(key);
			if (pending) return pending;
			const request = createWorker(options).finally(() => pendingSpawns.delete(key));
			pendingSpawns.set(key, request);
			return request;
		},
		deleteSession: async (id) => { await client.deleteSession(orgId, id); },
		getConversationPage: async (id, _before, options) => {
			requireCurrent();
			const cached = cloudConversationReplayCache.get(scopeKey, id);
			const [{ session }, { events, latestSequence }, overrides] = await Promise.all([
				awaitCloudReplay(options ? client.getSession(orgId, id, options) : client.getSession(orgId, id), options?.signal),
				(subscriptions.has(id) && cached && !replays.has(id)
					? Promise.resolve(cached) : replay(id, options?.signal)).then((entry) => ({ events: entry.events, latestSequence: entry.cursor })),
				readSettings(id),
			]);
			requireCurrent(); options?.signal?.throwIfAborted();
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
				controller: { state: session.isTerminated ? "stopped" as const : cloudHeaderControllerState(cloudLifecycleStage(toDashboardSession(session)), turns.some((turn) => turn.state === "running" || turn.state === "queued") ? "busy" : "ready") },
				latestSequence,
				oldestSequence: events.length > 0 ? events[0].sequence : latestSequence,
				// No backward pagination yet: a full forward replay is the whole
				// transcript the control plane will hand back today.
				hasMoreBefore: false,
				turns,
				items: toConversationItems(events),
				settings: { ...(session.model ? { model: session.model } : {}), ...overrides },
				capabilities: cloudConversationCapabilities(session, events),
			};
		},
		sendMessage: async (id, input) => {
			const settings = input.settings === undefined ? await readSettings(id) : normalizeSettings(input.settings);
			requireCurrent();
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
		steerMessage: async (id, input, targetTurnId) => {
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			const [{ session }, { events }] = await Promise.all([client.getSession(orgId, id), replay(id)]);
			if (session.activeTurn?.id !== targetTurnId || !cloudConversationCapabilities(session, events).includes("steer")) throw new Error("The turn finished or cannot take guidance now. Queue a new message instead.");
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			await client.steerTurn(orgId, id, targetTurnId, input.text, { idempotencyKey: input.clientMessageId });
			return { duplicate: false, turnId: targetTurnId };
		},
		// Returns 202 with a body (ResumeSessionResponse), not 204 — the
		// reconciler owns the actual provider/worker transition from here, so
		// the response body is discarded and the caller relies on the polled
		// lifecycle stage (cloudLifecycleStage) to see it land.
		resumeSession: async (id) => {
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			await client.resumeSession(orgId, id);
		},
		restoreSession: async (id) => {
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			await client.restoreSession(orgId, id);
		},
		retryStartup: async (id) => {
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			const { session } = await client.getSession(orgId, id);
			if (session.isTerminated || session.desiredState !== "running" || cloudStartupProblem(toDashboardSession(session))?.phase !== "failed") throw new Error("This session is not waiting on a failed startup. Refresh its status.");
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			try { await client.retrySessionStartup(orgId, id); }
			catch (cause) {
				if (cause instanceof CloudApiError && cause.status === 409 && cause.code === "startup_retry_unavailable") await client.getSession(orgId, id);
				throw cause;
			}
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
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			const normalized = normalizeSettings(settings);
			await afterDraftWrites(settingsKey(id), () => AsyncStorage.setItem(settingsKey(id), JSON.stringify(normalized)));
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			settingsBySession.set(id, normalized);
		},
		cancelTurn: async (id, turnId) => {
			// Keyed by the turn id, not freshly generated per call, so a retried
			// cancel of the same turn is idempotent instead of firing a second
			// distinct request.
			await client.cancelTurn(orgId, id, turnId, { idempotencyKey: `cancel-${turnId}` });
		},
		touchPresence: async (id, options) => {
			requireCurrent();
			await client.touchSessionPresence(orgId, id, options);
		},
		subscribeEvents: (id, listener, options) => {
			const controller = new AbortController();
			const unwatch = cloudConversationReplayCache.watchScope(scopeKey, () => controller.abort());
			const unpin = cloudConversationReplayCache.pin(scopeKey, id);
			subscriptions.set(id, (subscriptions.get(id) ?? 0) + 1);
			void pollCloudEvents({
				client,
				orgId,
				sessionId: id,
				after: cloudConversationReplayCache.get(scopeKey, id)?.cursor ?? 0,
				signal: controller.signal,
				intervalMs: CLOUD_EVENT_POLL_MS,
				readPage: async (after, signal) => {
					if (!isCurrent()) { controller.abort(); requireCurrent(); }
					const entry = await replay(id, signal);
					return { events: entry.events.filter((event) => event.sequence > after), nextAfter: entry.cursor, hasMore: false };
				},
				onError: options?.onError,
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
			let closed = false;
			return () => {
				if (closed) return; closed = true; controller.abort(); unpin(); unwatch();
				const count = (subscriptions.get(id) ?? 1) - 1;
				if (count) subscriptions.set(id, count); else subscriptions.delete(id);
			};
		},
	};
}
