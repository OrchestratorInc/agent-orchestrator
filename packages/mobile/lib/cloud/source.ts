import { CloudApiError, type ChatMessageInput, type ChatModelsResponse, type CloudClient, type CreateSessionInput, type Session, type Turn } from "@aoagents/cloud-client";
import type { DashboardSession, ProjectInfo, SpawnOptions } from "../api";
import type { ConversationEvent } from "../chat/sse";
import type { ApprovalMode, ChatConfigOption, ConversationTurn, TurnSettings } from "../chat/types";
import type { SessionSource } from "../environment/types";
import { fetchConversationReplay, pollCloudEvents, toConversationItems, toConversationTurns } from "./events";
import { toDashboardSession, toProjectInfo } from "./mapping";
import { cloudRoleDefaults } from "../spawnModel";
import { acceptCloudOperation, prepareCloudOperation, readCloudOperation, rejectCloudOperation, reserveCloudOperation } from "./operation";
import { isCloudSessionLimitError } from "./errorCopy";
import { cloudHeaderControllerState, cloudLifecycleStage, cloudStartupProblem, isWorkspaceStartupError } from "./lifecycle";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { afterDraftWrites } from "../chat/pendingSend";
import { cloudConversationCapabilities } from "./events";
import { awaitCloudReplay, cloudConversationReplayCache, type CloudReplayEntry } from "./conversationReplay";
import { cloudInterfaceDelay, cloudInterfaceInputLocked, cloudInterfaceSettledTarget, mergeCloudInterfaceStatus, type CloudInterfaceStatus } from "./interfaceTransition";
import type { RequestOptions, SessionInterfaceTransition, StartSessionInterfaceTransitionInput } from "@aoagents/cloud-client";
import { mobileInterfaceTransitionIsActive, mobileInterfaceTransitionIsCancellable } from "../session/interfaceTransition";
import { throwIfCloudRequestAborted } from "./cancellation";
import { createCloudWorkspaceSource } from "./workspace";
import { createCloudPullRequestSource } from "./pullRequests";
import { createCloudPreviewSource } from "./browser";

/** How often the cloud transcript is polled for new events. Matches the
 * tunnel-path conversation poll interval mobile already uses elsewhere. */
const CLOUD_EVENT_POLL_MS = 2_000;
const CHAT_HANDOFF_POLL_MS = 1_000;
const CHAT_HANDOFF_TIMEOUT_MS = 60_000;
const reasoningEfforts = new Set(["default", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]);

function allowedApprovalModes(session: Session): ApprovalMode[] {
	if (session.mode === "read-only" || !session.mode) return [];
	if (session.mode === "standard") return session.harness === "codex" ? ["accept-edits", "auto"] : ["default", "accept-edits", "auto"];
	return ["default", "accept-edits", "auto", "bypass-permissions"];
}

function effectiveTurnSettings(session: Session, overrides: TurnSettings): TurnSettings {
	const { approvalMode: savedApproval, ...settings } = overrides;
	const allowed = allowedApprovalModes(session);
	const approvalMode = savedApproval && allowed.includes(savedApproval) ? savedApproval : allowed[0];
	return {
		...(session.model ? { model: session.model } : {}), ...settings,
		...(approvalMode && (savedApproval || approvalMode !== "default") ? { approvalMode } : {}),
	};
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
	const transitionStatus = new Map<string, CloudInterfaceStatus>();
	const transitionReadSequence = new Map<string, number>();
	const transitionStarts = new Map<string, { target: string; promise: Promise<{ transition: SessionInterfaceTransition }> }>();
	const getInterfaceTransition = async (id: string, options?: RequestOptions): Promise<CloudInterfaceStatus> => {
		requireCurrent(); throwIfCloudRequestAborted(options?.signal);
		const sequence = (transitionReadSequence.get(id) ?? 0) + 1; transitionReadSequence.set(id, sequence);
		const [{ session }, wire] = await Promise.all([client.getSession(orgId, id, options), client.getSessionInterfaceTransition(orgId, id, options)]);
		requireCurrent(); throwIfCloudRequestAborted(options?.signal);
		const next = mergeCloudInterfaceStatus(transitionStatus.get(id), wire, session);
		if (transitionReadSequence.get(id) !== sequence) return transitionStatus.get(id) ?? next;
		transitionStatus.set(id, next); return next;
	};
	const startInterfaceTransition = (id: string, request: StartSessionInterfaceTransitionInput, options?: RequestOptions): Promise<{ transition: SessionInterfaceTransition }> => {
		const existing = transitionStarts.get(id);
		if (existing) return existing.target === request.targetMode ? existing.promise : Promise.reject(new Error("This session is already switching to the other interface."));
		const promise = (async () => {
			const status = await getInterfaceTransition(id, options);
			if (mobileInterfaceTransitionIsActive(status.transition)) {
				if (status.transition!.targetMode !== request.targetMode) throw new Error("This session is already switching to the other interface.");
				return { transition: status.transition! };
			}
			if (status.transition?.phase === "recovery_required") throw new Error(status.transition.errorDetail || "This session needs interface recovery before switching again.");
			if (!status.supported) throw new Error(status.reason || "Cloud handoff is unavailable for this agent.");
			if (request.targetMode === "chat" && (request.model || request.reasoningEffort)) throw new Error("Model overrides are supported only when switching to Terminal.");
			requireCurrent(); throwIfCloudRequestAborted(options?.signal);
			const result = options ? await client.startSessionInterfaceTransition(orgId, id, request, options) : await client.startSessionInterfaceTransition(orgId, id, request);
			requireCurrent(); throwIfCloudRequestAborted(options?.signal);
			transitionReadSequence.set(id, (transitionReadSequence.get(id) ?? 0) + 1);
			transitionStatus.set(id, { ...status, transition: result.transition });
			return result;
		})().finally(() => { if (transitionStarts.get(id)?.promise === promise) transitionStarts.delete(id); });
		transitionStarts.set(id, { target: request.targetMode, promise }); return promise;
	};
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
				requireCurrent(); throwIfCloudRequestAborted(controller.signal);
				return cloudConversationReplayCache.apply(scopeKey, id, events, latestSequence);
			}).finally(() => { unwatch(); if (replays.get(id) === created) replays.delete(id); });
			request = created; replays.set(id, created);
		}
		request.consumers++;
		try { return await awaitCloudReplay(request.promise, signal); }
		finally { if (--request.consumers === 0) request.controller.abort(); }
	};
	const settingsBySession = new Map<string, TurnSettings>();
	const settingsRevision = new Map<string, number>();
	const discoverySequence = new Map<string, number>();
	const requireDiscovery = (id: string, sequence: number | undefined) => {
		if (sequence !== undefined && discoverySequence.get(id) !== sequence) throw Object.assign(new Error("Cloud turn settings discovery was superseded."), { name: "AbortError" });
	};
	const settingsKey = (id: string) => "ao.cloud.turn-settings.v1." + encodeURIComponent(JSON.stringify([scopeKey, id]));
	const normalizeSettings = (value: TurnSettings): TurnSettings => ({
		...(typeof value.model === "string" && value.model.trim() ? { model: value.model.trim() } : {}),
		...(typeof value.reasoningEffort === "string" && reasoningEfforts.has(value.reasoningEffort) ? { reasoningEffort: value.reasoningEffort } : {}),
		...(value.approvalMode && ["default", "accept-edits", "auto", "bypass-permissions"].includes(value.approvalMode) ? { approvalMode: value.approvalMode } : {}),
		...(value.executionMode === "agent" || value.executionMode === "plan" ? { executionMode: value.executionMode } : {}),
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
	const writeSettings = async (id: string, settings: TurnSettings, discovery?: { sequence: number; selectionRevision: number }) => {
		requireCurrent();
		if (!discovery && ("model" in settings || "reasoningEffort" in settings)) settingsRevision.set(id, (settingsRevision.get(id) ?? 0) + 1);
		await readSettings(id);
		await afterDraftWrites(settingsKey(id), async () => {
			requireCurrent();
			if (discovery) {
				requireDiscovery(id, discovery.sequence);
				if (discovery.selectionRevision !== (settingsRevision.get(id) ?? 0)) return;
			}
			const normalized = normalizeSettings({ ...settingsBySession.get(id), ...settings });
			await AsyncStorage.setItem(settingsKey(id), JSON.stringify(normalized));
			requireCurrent();
			settingsBySession.set(id, normalized);
		});
	};
	const listChatModels = async (id: string): Promise<ChatModelsResponse> => {
		try { return await client.listChatModels(orgId, id); }
		catch (cause) {
			if (!(cause instanceof CloudApiError) || cause.code !== "WORKER_UNAVAILABLE") throw cause;
		}
		requireCurrent();
		await client.resumeSession(orgId, id);
		for (let attempt = 0; attempt < 20; attempt++) {
			requireCurrent();
			await cloudInterfaceDelay(500);
			requireCurrent();
			try { return await client.listChatModels(orgId, id); }
			catch (cause) {
				if (!(cause instanceof CloudApiError) || cause.code !== "WORKER_UNAVAILABLE" || attempt === 19) throw cause;
			}
		}
		throw new Error("The Cloud worker did not become available.");
	};
	const cloudConfigOptions = (session: Session, catalog: ChatModelsResponse, settings: TurnSettings): ChatConfigOption[] =>
		session.harness === "claude-code" && catalog.modes?.includes("plan") ? [{
			id: "cloud-execution-mode", name: "Mode", category: "execution_mode", type: "select",
			currentValue: session.mode === "read-only" || settings.executionMode === "plan" ? "plan" : "agent",
			choices: [
				...(session.mode !== "read-only" && catalog.modes.includes("default") ? [{ value: "agent", name: "Agent" }] : []),
				{ value: "plan", name: "Plan" },
			],
		}] : [];
	const getChatTurnOptions = async (id: string, options?: { adoptNativeSettings?: boolean }) => {
		requireCurrent();
		const sequence = options?.adoptNativeSettings === false ? undefined : (discoverySequence.get(id) ?? 0) + 1;
		if (sequence !== undefined) discoverySequence.set(id, sequence);
		const selectionRevision = settingsRevision.get(id) ?? 0;
		const { session } = await client.getSession(orgId, id);
		requireCurrent();
		const catalog: ChatModelsResponse = session.harness === "codex" || session.harness === "claude-code"
			? await listChatModels(id) : { models: [] };
		requireCurrent();
		requireDiscovery(id, sequence);
		// Reopening Chat or explicitly refreshing follows the native controller.
		// A phone selection made while that discovery ran still wins the race.
		if (sequence !== undefined && selectionRevision === (settingsRevision.get(id) ?? 0) && (catalog.model || catalog.reasoningEffort)) {
			await writeSettings(id, { ...(catalog.model ? { model: catalog.model } : {}), reasoningEffort: catalog.reasoningEffort || undefined }, { sequence, selectionRevision });
		}
		const settings = effectiveTurnSettings(session, await readSettings(id));
		requireDiscovery(id, sequence);
		return { models: catalog.models, configOptions: cloudConfigOptions(session, catalog, settings), settings };
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
				// Match desktop's Cloud task composer: prompt-derived titles, or
				// the selected harness for an idle worker with no instructions yet.
				displayName: (options.prompt ?? "").trim().slice(0, 100) || harness, prompt: options.prompt ?? "",
				...(options.mode ? { interfaceMode: options.mode } : {}),
				mode: "trusted", deniedCommands: [], model: options.model ?? defaults.model,
				reasoningEffort: options.reasoningEffort ?? defaults.reasoningEffort,
			} satisfies CreateSessionInput, () => options.clientRequestId!);
		}
		if (!isCurrent()) throw new Error("Cloud account changed. Reopen the task sheet.");
		await reserveCloudOperation(slot, operation);
		if (!isCurrent()) throw new Error("Cloud account changed. Reopen the task sheet.");
		const { session } = await client.createSession(orgId, operation.payload, { idempotencyKey: operation.key }).catch(async (cause: unknown) => {
			// Quota is rejected transactionally after idempotency replay. Interface
			// validation runs before replay, so only a fresh request proves that
			// no earlier attempt could have created the Chat session.
			if (isCloudSessionLimitError(cause) || !existing && cause instanceof CloudApiError && cause.status === 409 && cause.code === "SESSION_MODE_UNSUPPORTED") {
				try { await rejectCloudOperation(scopeKey, slot, operation.key); } catch {}
			}
			throw cause;
		});
		// Acceptance precedes board refresh. Storage failure cannot undo a server
		// acceptance; its reserved key still makes a future retry safe.
		acceptedSpawns.set(operation.key, session.id);
		try { await acceptCloudOperation(scopeKey, slot, operation.key, session.id); } catch {}
		return { id: session.id };
	};
	return {
		kind: "cloud",
		scopeKey,
		workspace: createCloudWorkspaceSource(input),
		pullRequests: createCloudPullRequestSource(input),
		preview: createCloudPreviewSource(input),
		getInterfaceTransition,
		startInterfaceTransition,
		cancelInterfaceTransition: async (id, options) => {
			const status = await getInterfaceTransition(id, options);
			if (!mobileInterfaceTransitionIsCancellable(status.transition)) throw new Error("The source controller is already stopping. This switch can no longer be cancelled.");
			requireCurrent(); throwIfCloudRequestAborted(options?.signal);
			const result = await client.cancelSessionInterfaceTransition(orgId, id, options);
			requireCurrent();
			transitionStatus.set(id, { ...status, transition: { ...status.transition!, phase: "cancelled" } });
			return result;
		},
		acknowledgeInterfaceNotice: async (id, transitionId, options) => {
			requireCurrent(); throwIfCloudRequestAborted(options?.signal);
			const result = await client.acknowledgeSessionInterfaceTransitionNotice(orgId, id, transitionId, options);
			requireCurrent();
			const previous = transitionStatus.get(id);
			if (previous?.transition?.id === transitionId) transitionStatus.set(id, { ...previous, transition: { ...previous.transition, noticeAcknowledgedAt: new Date().toISOString() } });
			return result;
		},
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
			requireCurrent(); throwIfCloudRequestAborted(options?.signal);
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
				items: toConversationItems(events, { startupFailed: !session.isTerminated && session.desiredState === "running" && isWorkspaceStartupError(session.startupError?.code) && Boolean(session.startupError?.message?.trim()) }),
				settings: effectiveTurnSettings(session, overrides),
				approvalModes: allowedApprovalModes(session),
				capabilities: cloudConversationCapabilities(session, events),
			};
		},
		sendMessage: async (id, input) => {
			const settings = input.settings === undefined ? await readSettings(id) : normalizeSettings(input.settings);
			requireCurrent();
			let mode: "read-only" | undefined;
			let approvalMode = settings.approvalMode;
			if (settings.executionMode || approvalMode) {
				const { session } = await client.getSession(orgId, id);
				requireCurrent();
				approvalMode = effectiveTurnSettings(session, settings).approvalMode;
				if (session.mode === "read-only") mode = "read-only";
				else if (settings.executionMode === "plan") {
					const catalog = session.harness === "claude-code" ? await listChatModels(id) : undefined;
					requireCurrent();
					if (!catalog?.modes?.includes("plan")) throw new Error("Plan mode is unavailable for this Cloud session. Refresh turn settings.");
					mode = "read-only";
				}
			}
			const message = settings && (settings.model || settings.reasoningEffort || settings.approvalMode || mode)
				? {
					text: input.text,
					...(settings.model ? { model: settings.model } : {}),
					...(settings.reasoningEffort && reasoningEfforts.has(settings.reasoningEffort)
						? { reasoningEffort: settings.reasoningEffort as ChatMessageInput["reasoningEffort"] } : {}),
					...(mode ? { mode } : {}),
					...(approvalMode && mode !== "read-only" ? { approvalMode } : {}),
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
			requireCurrent();
			const [{ session }, { events }] = await Promise.all([client.getSession(orgId, id), replay(id)]);
			requireCurrent();
			const matches = (event: { sessionId?: string; payload?: unknown }) => {
				const payload = event.payload as Record<string, unknown> | undefined;
				return event.sessionId === id && payload?.turnId === targetTurnId && payload?.clientMessageId === input.clientMessageId;
			};
			// A retry must still recover an accepted command after its turn ends.
			// The server returns that command's original event for the same key.
			const requested = events.some((event) => matches(event) && String(event.type) === "chat.turn_steer_requested" && (event.payload as Record<string, unknown>).text === input.text);
			if (!requested && (session.activeTurn?.id !== targetTurnId || !cloudConversationCapabilities(session, events).includes("steer"))) throw new Error("The turn finished or cannot take guidance now. Queue a new message instead.");
			const unknown = () => new Error("The provider's steer result is still unknown. Your text was not queued as a new turn. Retry this guidance to check its result.");
			const controller = new AbortController();
			const unwatch = cloudConversationReplayCache.watchScope(scopeKey, () => controller.abort());
			let timedOut = false;
			const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 30_000);
			try {
				const accepted = await awaitCloudReplay(client.steerTurn(orgId, id, targetTurnId, input.text, { idempotencyKey: input.clientMessageId, signal: controller.signal }), controller.signal);
				requireCurrent(); throwIfCloudRequestAborted(controller.signal);
				// HTTP 202 records a request, not provider acceptance. Its cursor is
				// authoritative even on an idempotent retry of an older request.
				if (!accepted?.event || !matches(accepted.event) || !Number.isSafeInteger(accepted.event.sequence) || accepted.event.sequence < 1) throw unknown();
				let after = accepted.event.sequence;
				for (;;) {
					requireCurrent(); throwIfCloudRequestAborted(controller.signal);
					const page = await awaitCloudReplay(client.replayEvents(orgId, id, { after, limit: 100, signal: controller.signal }), controller.signal);
					requireCurrent(); throwIfCloudRequestAborted(controller.signal);
					for (const event of page.events) {
						if (event.sequence <= after || !matches(event)) continue;
						if (String(event.type) === "chat.turn_steered") return { duplicate: false, turnId: targetTurnId };
						if (String(event.type) === "chat.turn_steer_failed") {
							const error = (event.payload as Record<string, unknown>).error;
							throw new Error(typeof error === "string" && error ? error : "The provider declined the steer.");
						}
					}
					if (page.hasMore && page.nextAfter <= after) throw unknown();
					after = Math.max(after, page.nextAfter);
					if (!page.hasMore) await cloudInterfaceDelay(500, controller.signal);
				}
			} catch (cause) {
				requireCurrent();
				if (timedOut) throw unknown();
				throw cause;
			} finally { clearTimeout(timer); unwatch(); controller.abort(); }
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
			const problem = cloudStartupProblem(toDashboardSession(session));
			if (session.isTerminated || session.desiredState !== "running" || problem?.phase !== "failed") throw new Error("This session is not waiting on a failed startup. Refresh its status.");
			if (!isCurrent()) throw new Error("Cloud account changed. Try again.");
			if (isWorkspaceStartupError(problem.code)) {
				await client.requestWorkspaceCheckout(orgId, id);
				return;
			}
			try { await client.retrySessionStartup(orgId, id); }
			catch (cause) {
				if (cause instanceof CloudApiError && cause.status === 409 && cause.code === "startup_retry_unavailable") await client.getSession(orgId, id);
				throw cause;
			}
		},
		switchToChat: async (id, options) => {
			requireCurrent();
			let status = await getInterfaceTransition(id, options);
			if (status.currentMode === "chat" && !cloudInterfaceInputLocked(status)) return;
			if (status.transition?.phase === "recovery_required") throw new Error(status.transition.errorDetail || "This session needs interface recovery.");
			if (mobileInterfaceTransitionIsActive(status.transition)) {
				if (status.transition!.targetMode !== "chat") throw new Error("This session is already switching to Terminal.");
			} else await startInterfaceTransition(id, { targetMode: "chat", policy: "drain" }, options);
			const deadline = Date.now() + CHAT_HANDOFF_TIMEOUT_MS;
			for (;;) {
				status = await getInterfaceTransition(id, options);
				if (["failed", "cancelled", "recovery_required"].includes(status.transition?.phase ?? "")) throw new Error(status.transition?.errorDetail || "Cloud could not switch this session to Chat.");
				if (cloudInterfaceSettledTarget(status) === "chat") return;
				if (Date.now() >= deadline) throw new Error("Cloud is still switching interfaces. Reopen the session to check its durable status.");
				await cloudInterfaceDelay(CHAT_HANDOFF_POLL_MS, options?.signal);
			}
		},
		decideApproval: async (id, requestId, decisionId) => {
			requireCurrent();
			await client.decideChatApproval(orgId, id, requestId, decisionId);
			requireCurrent();
		},
		getChatModels: async (id) => (await getChatTurnOptions(id)).models,
		getChatTurnOptions,
		setChatConfigOption: async (id, optionId, value) => {
			const { configOptions } = await getChatTurnOptions(id, { adoptNativeSettings: false });
			const option = configOptions.find((option) => option.id === optionId);
			if (!("value" in value) || !option?.choices.some((choice) => choice.value === value.value)) throw new Error("This Cloud turn option is unavailable. Refresh turn settings.");
			await writeSettings(id, { executionMode: value.value as "agent" | "plan" });
			return configOptions.map((option) => option.id === optionId ? { ...option, currentValue: value.value } : option);
		},
		setTurnSettings: writeSettings,
		cancelTurn: async (id, turnId) => {
			requireCurrent();
			// Keyed by the turn id, not freshly generated per call, so a retried
			// cancel of the same turn is idempotent instead of firing a second
			// distinct request.
			await client.cancelTurn(orgId, id, turnId, { idempotencyKey: `cancel-${turnId}` });
			requireCurrent();
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
