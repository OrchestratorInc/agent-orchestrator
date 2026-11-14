import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { aoBridge } from "./bridge";
import { eventStreamBaseUrl, getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { setEventsConnectionState } from "./events-connection";
import { computeSseRetryDelayMs } from "./sse-backoff";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { conversationQueryKey, conversationQueryRoot, conversationSettled, refreshRemoteConversationOnce } from "../hooks/useConversation";
import {
	reviewerConversationQueryKey,
	reviewerConversationQueryRoot,
} from "../hooks/useReviewerConversation";
import { sessionReviewsQueryKey } from "./session-reviews";
import { sessionUsageQueryRoot } from "../hooks/useSessionUsageSummaries";
import { editorHandoffQueryKey, editorHandoffQueryRoot } from "../hooks/useEditorHandoff";
import { shellTerminalsQueryKey, shellTerminalsQueryKeyForHost } from "../hooks/useShellTerminals";
import { baseUrlForHost, connectedHosts, subscribeConnectedHosts } from "./host-clients";
import { probeRemoteSse } from "./remote-sse-probe";
import { persistRemoteCache } from "./remote-chat-cache";

export type EventTransport = {
	connect: () => () => void;
};

const INVALIDATE_WINDOW_MS = 150;
const ID_ONLY_WORKSPACE_REFRESH_MS = 5_000;
const EVENTSOURCE_CLOSED = 2;

const CDC_EVENT_TYPES = [
	"session_created",
	"session_updated",
	"pr_created",
	"pr_updated",
	"pr_check_recorded",
	"pr_session_changed",
	"pr_review_thread_added",
	"pr_review_thread_resolved",
	"review_run_created",
	"review_run_updated",
] as const;

export function createEventTransport(queryClient: QueryClient): EventTransport {
	return {
		connect() {
			let healthAttempt = 0;
			let refreshTimer: ReturnType<typeof setTimeout> | undefined;
			const pendingConversationSessions = new Set<string>();
			const pendingReviewerConversations = new Set<string>();
			const pendingReviewSessions = new Set<string>();
			const pendingInterfaceTransitionSessions = new Set<string>();
			const pendingEditorHandoffSessions = new Set<string>();
			const pendingModelCatalogScopes = new Map<string, { agentId: string; projectId: string }>();
			let workspaceInvalidationPending = false;
			let allConversationsInvalidationPending = false;
			let allEditorHandoffsInvalidationPending = false;
			let allEventScopedCachesInvalidationPending = false;
			let idOnlyWorkspaceRefreshPending = false;
			let idOnlyWorkspaceTimer: ReturnType<typeof setTimeout> | undefined;
			let lastWorkspaceRefreshAt = Number.NEGATIVE_INFINITY;
			let retryTimer: ReturnType<typeof setTimeout> | undefined;
			let source: EventSource | undefined;
			let sourceBaseUrl: string | undefined;
			let cursorUnsupportedBaseUrl: string | undefined;
			const probeCursorSupport = (baseUrl: string, url: string) => {
				const controller = new AbortController();
				void fetch(url, { signal: controller.signal })
					.then((response) => {
						if (response.status === 400) cursorUnsupportedBaseUrl = baseUrl;
					})
					.catch(() => undefined)
					.finally(() => controller.abort());
			};
			const stopPersistingRemoteCache = persistRemoteCache(queryClient);
			let disposed = false;
			type RemoteConnection = {
				base: string;
				close?: () => void;
				pollTimer?: ReturnType<typeof setInterval>;
				lastRefreshAt: number;
				idOnlyTimer?: ReturnType<typeof setTimeout>;
				failures: number;
				reopenTimer?: ReturnType<typeof setTimeout>;
			};
			const remoteSources = new Map<string, RemoteConnection>();
			const refreshOpenRemoteChat = (hostId: string, sessionId: string) => {
				if (disposed || !baseUrlForHost(hostId)) return;
				if (!queryClient.getQueryCache().find({ queryKey: conversationQueryKey(sessionId, hostId), type: "active" })) return;
				refreshRemoteConversationOnce(queryClient, sessionId, hostId);
			};
			const refreshOpenRemoteChats = (hostId: string) => {
				for (const query of queryClient.getQueryCache().findAll({ queryKey: ["remote-conversation", hostId], type: "active" })) {
					const sessionId = query.queryKey[2];
					if (typeof sessionId === "string") refreshRemoteConversationOnce(queryClient, sessionId, hostId);
				}
			};
			const refreshes = new Map<string, { dirty: boolean }>();
			const invalidate = (queryKey: QueryKey) => {
				if (disposed) return;
				const key = JSON.stringify(queryKey);
				const running = refreshes.get(key);
				if (running) {
					running.dirty = true;
					return;
				}
				const state = { dirty: queryClient.isFetching({ queryKey, type: "active" }) > 0 };
				if (queryKey[0] === conversationQueryRoot[0] && queryClient.isFetching({ queryKey, type: "inactive" }) > 0) {
					void queryClient.cancelQueries({ queryKey, type: "inactive" });
				}
				refreshes.set(key, state);
				const settled = () => {
					refreshes.delete(key);
					if (state.dirty && !disposed) invalidate(queryKey);
				};
				void queryClient.invalidateQueries({ queryKey }, { cancelRefetch: false }).then(settled, settled);
			};
			const refreshRemote = (hostId: string, reconnect = false) => {
				const connection = remoteSources.get(hostId);
				if (connection) connection.lastRefreshAt = Date.now();
				invalidate(["remote-workspaces", hostId]);
				invalidate(["project", hostId]);
				invalidate(["project-config", hostId]);
				if (reconnect) invalidate(["remote-conversation", hostId]);
				if (reconnect) invalidate(["reviewer-conversation", hostId]);
				invalidate(["session-scm-summary", hostId]);
				invalidate(["session-reviews", hostId]);
				invalidate(["session-usage", hostId]);
				invalidate(["session-usage", "detail", hostId]);
				invalidate(["session-interface-transition", hostId]);
				invalidate(["agent-readiness", hostId]);
				invalidate(["agent-models", hostId]);
			};
			const pollRemote = (hostId: string) => {
				refreshRemote(hostId);
				invalidate(["reviewer-conversation", hostId]);
				refreshOpenRemoteChats(hostId);
			};
			const refreshRemoteForIdOnlyUpdate = (hostId: string, connection: RemoteConnection) => {
				const wait = connection.lastRefreshAt + ID_ONLY_WORKSPACE_REFRESH_MS - Date.now();
				if (wait <= 0) {
					refreshRemote(hostId);
					return;
				}
				connection.idOnlyTimer ??= setTimeout(() => {
					connection.idOnlyTimer = undefined;
					if (!disposed && remoteSources.get(hostId) === connection) refreshRemote(hostId);
				}, wait);
			};
			const connectRemote = (hostId: string) => {
				if (disposed) return;
				const base = baseUrlForHost(hostId);
				if (!base) return;
				if (remoteSources.get(hostId)?.base === base) return;
				const connection: RemoteConnection = { base, lastRefreshAt: Number.NEGATIVE_INFINITY, failures: 0 };
				remoteSources.set(hostId, connection);
				refreshRemote(hostId, true);
				connection.pollTimer = setInterval(() => pollRemote(hostId), 2_000);
				const openStream = () => { connection.close = probeRemoteSse(
					`${base.replace(/\/+$/, "")}/api/v1/events?after=latest`,
					CDC_EVENT_TYPES,
					(event) => {
						if (disposed || remoteSources.get(hostId) !== connection) return;
						let conversationOnly = false;
						let idOnlySessionUpdate = false;
						if ("data" in event) {
							try {
								const decoded = JSON.parse(String((event as MessageEvent).data)) as {
									type?: unknown;
									sessionId?: unknown;
									payload?: { conversationId?: unknown; reviewId?: unknown; interfaceTransitionId?: unknown; shellTerminalHandleId?: unknown };
								};
								const payload = typeof decoded.payload === "object" && decoded.payload !== null ? decoded.payload : undefined;
								const sessionId = typeof decoded.sessionId === "string" && decoded.sessionId ? decoded.sessionId : undefined;
								if (typeof payload?.reviewId === "string") {
									invalidate(["reviewer-conversation", hostId, payload.reviewId]);
									conversationOnly = typeof payload.conversationId === "string";
								} else if (sessionId && typeof payload?.conversationId === "string") {
									refreshOpenRemoteChat(hostId, sessionId);
									conversationOnly = true;
								} else if (typeof payload?.shellTerminalHandleId === "string") {
									invalidate(shellTerminalsQueryKeyForHost(hostId));
									conversationOnly = true;
								} else if (decoded.type === "session_updated" && sessionId && typeof payload?.interfaceTransitionId !== "string") {
									idOnlySessionUpdate = payload !== undefined && Object.keys(payload).every((key) => key === "id" || key === "sessionId");
									if (!idOnlySessionUpdate || !conversationSettled(queryClient, sessionId, hostId)) refreshOpenRemoteChat(hostId, sessionId);
								}
							} catch {
							}
						}
						if (conversationOnly) return;
						if (idOnlySessionUpdate) refreshRemoteForIdOnlyUpdate(hostId, connection);
						else refreshRemote(hostId);
					},
					() => {
						if (remoteSources.get(hostId) !== connection) return;
						connection.failures = 0;
						if (connection.pollTimer !== undefined) clearInterval(connection.pollTimer);
						connection.pollTimer = undefined;
						refreshRemote(hostId, true);
						refreshOpenRemoteChats(hostId);
					},
					() => {
						if (remoteSources.get(hostId) !== connection) return;
						if (connection.pollTimer === undefined) connection.pollTimer = setInterval(() => pollRemote(hostId), 2_000);
						refreshRemote(hostId);
						connection.failures += 1;
						connection.reopenTimer ??= setTimeout(() => {
							connection.reopenTimer = undefined;
							if (!disposed && remoteSources.get(hostId) === connection) openStream();
						}, computeSseRetryDelayMs(connection.failures));
					},
				); };
				openStream();
			};
			const syncRemoteSources = () => {
				const active = new Set(connectedHosts());
				for (const [hostId, connection] of remoteSources) {
					if (active.has(hostId) && connection.base === baseUrlForHost(hostId)) continue;
					connection.close?.();
					if (connection.pollTimer !== undefined) clearInterval(connection.pollTimer);
					if (connection.idOnlyTimer !== undefined) clearTimeout(connection.idOnlyTimer);
					remoteSources.delete(hostId);
				}
				for (const hostId of active) connectRemote(hostId);
			};
			let lastFlushAt = Number.NEGATIVE_INFINITY;
			const flushPending = () => {
				if (allConversationsInvalidationPending) {
					invalidate(conversationQueryRoot);
					invalidate(reviewerConversationQueryRoot);
					allConversationsInvalidationPending = false;
				}
				if (workspaceInvalidationPending) {
					invalidate(workspaceQueryKey);
					invalidate(sessionScmSummaryQueryKey());
					invalidate(sessionUsageQueryRoot);
					workspaceInvalidationPending = false;
					lastWorkspaceRefreshAt = Date.now();
					idOnlyWorkspaceRefreshPending = false;
				}
				if (allEventScopedCachesInvalidationPending) {
					invalidate(["session-reviews"]);
					invalidate(["session-interface-transition"]);
					invalidate(["agent-models"]);
					allEventScopedCachesInvalidationPending = false;
				}
				if (allEditorHandoffsInvalidationPending) {
					invalidate(editorHandoffQueryRoot);
					allEditorHandoffsInvalidationPending = false;
					pendingEditorHandoffSessions.clear();
				} else {
					for (const sessionId of pendingEditorHandoffSessions) {
						invalidate(editorHandoffQueryKey(sessionId));
					}
					pendingEditorHandoffSessions.clear();
				}
				for (const sessionId of pendingConversationSessions) {
					invalidate(conversationQueryKey(sessionId));
				}
				pendingConversationSessions.clear();
				for (const sessionId of pendingReviewSessions) {
					invalidate(sessionReviewsQueryKey(sessionId));
				}
				pendingReviewSessions.clear();
				for (const reviewId of pendingReviewerConversations) {
					invalidate(reviewerConversationQueryKey(reviewId));
				}
				pendingReviewerConversations.clear();
				for (const sessionId of pendingInterfaceTransitionSessions) {
					invalidate(["session-interface-transition", sessionId]);
				}
				pendingInterfaceTransitionSessions.clear();
				for (const scope of pendingModelCatalogScopes.values()) {
					invalidate(["agent-models", scope.agentId]);
				}
				pendingModelCatalogScopes.clear();
			};
			const scheduleFlush = () => {
				if (refreshTimer !== undefined) return;
				const sinceLastFlush = Date.now() - lastFlushAt;
				if (sinceLastFlush >= INVALIDATE_WINDOW_MS) {
					lastFlushAt = Date.now();
					flushPending();
					return;
				}
				refreshTimer = setTimeout(() => {
					refreshTimer = undefined;
					lastFlushAt = Date.now();
					flushPending();
				}, INVALIDATE_WINDOW_MS - sinceLastFlush);
			};
			const queueIdOnlyWorkspaceRefresh = () => {
				idOnlyWorkspaceRefreshPending = true;
				const wait = lastWorkspaceRefreshAt + ID_ONLY_WORKSPACE_REFRESH_MS - Date.now();
				if (wait <= 0) {
					workspaceInvalidationPending = true;
					return;
				}
				if (idOnlyWorkspaceTimer !== undefined) return;
				idOnlyWorkspaceTimer = setTimeout(() => {
					idOnlyWorkspaceTimer = undefined;
					if (!idOnlyWorkspaceRefreshPending) return;
					queueIdOnlyWorkspaceRefresh();
					if (workspaceInvalidationPending) scheduleFlush();
				}, wait);
			};
			const refreshWorkspaces = (event?: Event) => {
				if (disposed) return;
				let conversationOnly = false;
				let modelCatalogOnly = false;
				let idOnlySessionUpdate = false;
				if (event === undefined) {
					allConversationsInvalidationPending = true;
					allEditorHandoffsInvalidationPending = true;
					allEventScopedCachesInvalidationPending = true;
				}
				if (event && "data" in event) {
					try {
						const decoded = JSON.parse(String((event as MessageEvent).data)) as {
							sessionId?: unknown;
							type?: unknown;
							payload?: unknown;
						};
						const payload =
							typeof decoded.payload === "object" && decoded.payload !== null
								? (decoded.payload as {
										conversationId?: unknown;
										reviewId?: unknown;
										interfaceTransitionId?: unknown;
										activity?: unknown;
										shellTerminalHandleId?: unknown;
										kind?: unknown;
										agentId?: unknown;
										projectId?: unknown;
								  })
								: undefined;
						if (typeof payload?.shellTerminalHandleId === "string") {
							void queryClient.invalidateQueries({ queryKey: shellTerminalsQueryKey });
							conversationOnly = true;
						}
						if (
							(decoded.type === "review_run_created" || decoded.type === "review_run_updated") &&
							typeof decoded.sessionId === "string" &&
							decoded.sessionId
						) {
							pendingReviewSessions.add(decoded.sessionId);
						}
						if (payload?.kind === "model_catalog" && typeof payload.agentId === "string" && typeof payload.projectId === "string") {
							pendingModelCatalogScopes.set(`${payload.agentId}\0${payload.projectId}`, {
								agentId: payload.agentId,
								projectId: payload.projectId,
							});
							modelCatalogOnly = true;
						}
						if (
							typeof payload?.reviewId === "string" &&
							payload.reviewId &&
							typeof payload.conversationId === "string" &&
							payload.conversationId
						) {
							pendingReviewerConversations.add(payload.reviewId);
							conversationOnly = true;
						} else if (
							typeof decoded.sessionId === "string" &&
							decoded.sessionId &&
							typeof payload?.interfaceTransitionId === "string" &&
							payload.interfaceTransitionId
						) {
							pendingInterfaceTransitionSessions.add(decoded.sessionId);
						}
						if (
							typeof decoded.sessionId === "string" &&
							decoded.sessionId &&
							typeof payload?.conversationId === "string" &&
							payload.conversationId
						) {
							pendingConversationSessions.add(decoded.sessionId);
							conversationOnly = true;
						}
						if (
							decoded.type === "session_updated" &&
							typeof decoded.sessionId === "string" &&
							decoded.sessionId &&
							typeof payload?.conversationId !== "string" &&
							typeof payload?.interfaceTransitionId !== "string" &&
							typeof payload?.shellTerminalHandleId !== "string"
						) {
							idOnlySessionUpdate =
								payload !== undefined && Object.keys(payload).every((key) => key === "id" || key === "sessionId");
							if (!idOnlySessionUpdate || !conversationSettled(queryClient, decoded.sessionId)) {
								pendingConversationSessions.add(decoded.sessionId);
							}
							if (typeof payload?.activity === "string") {
								pendingInterfaceTransitionSessions.add(decoded.sessionId);
							}
							pendingEditorHandoffSessions.add(decoded.sessionId);
						}
					} catch {
					}
				}
				if (idOnlySessionUpdate) queueIdOnlyWorkspaceRefresh();
				else if (!conversationOnly && !modelCatalogOnly) workspaceInvalidationPending = true;
				scheduleFlush();
			};

			let retries = 0;

			const scheduleRetry = () => {
				if (disposed || retryTimer) return;
				retries += 1;
				retryTimer = setTimeout(() => {
					retryTimer = undefined;
					connectSource();
				}, computeSseRetryDelayMs(retries));
			};

			const connectSource = () => {
				if (disposed || typeof EventSource === "undefined") return;
				if (!hasTrustedApiBaseUrl()) {
					healthAttempt += 1;
					source?.close();
					source = undefined;
					sourceBaseUrl = undefined;
					setEventsConnectionState("disconnected");
					return;
				}
				const baseUrl = getApiBaseUrl();
				if (source && sourceBaseUrl === baseUrl && source.readyState !== EVENTSOURCE_CLOSED) return;
				if (sourceBaseUrl && sourceBaseUrl !== baseUrl) retries = 0;
				source?.close();
				source = undefined;
				sourceBaseUrl = baseUrl;
				try {
					const streamUrl = `${eventStreamBaseUrl(baseUrl)}/api/v1/events`;
					const useCursor = cursorUnsupportedBaseUrl !== baseUrl;
					source = new EventSource(useCursor ? `${streamUrl}?after=latest` : streamUrl);
					const connectedSource = source;
					let opened = false;
					source.onopen = () => {
						if (disposed || source !== connectedSource) return;
						opened = true;
						healthAttempt += 1;
						retries = 0;
						setEventsConnectionState("connected");
						refreshWorkspaces();
					};
					source.onerror = () => {
						if (disposed || source !== connectedSource) return;
						setEventsConnectionState("disconnected");
						if (source?.readyState === EVENTSOURCE_CLOSED) {
							if (!opened && useCursor) probeCursorSupport(baseUrl, `${streamUrl}?after=latest`);
							scheduleRetry();
						}
						const attempt = ++healthAttempt;
						void queryClient.refetchQueries(
							{ queryKey: workspaceQueryKey, type: "active" },
							{ throwOnError: true },
						).then(
							() => {
								if (attempt !== healthAttempt || source !== connectedSource) return;
							},
							() => {
								if (attempt !== healthAttempt || source !== connectedSource) return;
							},
						);
					};
					source.onmessage = refreshWorkspaces;
					for (const type of CDC_EVENT_TYPES) {
						source.addEventListener(type, refreshWorkspaces);
					}
				} catch {
					source = undefined;
				}
			};

			let lastDaemonStatusKey: string | undefined;
			const removeDaemonListener = aoBridge.daemon.onStatus((status) => {
				const key = `${status?.state}:${status?.port ?? ""}:${status?.pid ?? ""}`;
				if (key === lastDaemonStatusKey) return;
				lastDaemonStatusKey = key;
				connectSource();
				refreshWorkspaces();
			});
			const removeBaseUrlListener = subscribeApiBaseUrl(connectSource);
			connectSource();
			const removeConnectedHostsListener = subscribeConnectedHosts(syncRemoteSources);
			syncRemoteSources();

			return () => {
				healthAttempt += 1;
				disposed = true;
				if (refreshTimer !== undefined) clearTimeout(refreshTimer);
				if (idOnlyWorkspaceTimer !== undefined) clearTimeout(idOnlyWorkspaceTimer);
				pendingConversationSessions.clear();
				pendingReviewSessions.clear();
				pendingInterfaceTransitionSessions.clear();
				pendingModelCatalogScopes.clear();
				refreshes.clear();
				if (retryTimer) clearTimeout(retryTimer);
				removeDaemonListener();
				removeBaseUrlListener();
				removeConnectedHostsListener();
				source?.close();
				for (const connection of remoteSources.values()) {
					connection.close?.();
					if (connection.pollTimer !== undefined) clearInterval(connection.pollTimer);
					if (connection.idOnlyTimer !== undefined) clearTimeout(connection.idOnlyTimer);
				}
				remoteSources.clear();
				stopPersistingRemoteCache();
				setEventsConnectionState("idle");
			};
		},
	};
}
