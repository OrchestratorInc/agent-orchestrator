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
// A session_updated that carries only the session id comes from usage
// bookkeeping (rarely an agent switch, cleanup fact or reported PR) and cannot
// say whether the board changed. During long agent runs these arrive faster than
// the window above and refetched the whole workspace every window for hours
// (#6763), so they refresh it at most once per this interval.
const ID_ONLY_WORKSPACE_REFRESH_MS = 5_000;
// EventSource.CLOSED, referenced numerically so test stubs without the static
// constants still work.
const EVENTSOURCE_CLOSED = 2;

// CDC event types the daemon pushes over the SSE stream (see
// backend/internal/cdc/event.go). The SSE writer tags each frame with
// `event: <type>`, so named events bypass EventSource.onmessage and must be
// subscribed explicitly. Every one of these can change the project/session list
// the sidebar renders, so they all trigger a (batched) workspace refetch.
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

/**
 * Wires live server state into the TanStack Query cache. Three sources feed it:
 *   - daemon lifecycle over Electron IPC (coming up/down changes session availability)
 *   - each connected daemon's CDC stream over SSE (project/session/PR changes)
 *   - the Codex account stream over SSE (account, capacity, and switch state)
 * Lifecycle and CDC events invalidate the owning host's cache; durable per-session
 * updates also refresh editor-handoff readiness. Invalidations are batched
 * because a single user action can emit a burst of CDC events.
 */
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
			// Reviews, interface transitions and model catalogs are otherwise only
			// refreshed by their own events. A fresh stream starts at the newest event,
			// so a lifecycle refresh must cover whatever changed in the gap.
			let allEventScopedCachesInvalidationPending = false;
			// An id-only update waits here until the workspace is a full
			// ID_ONLY_WORKSPACE_REFRESH_MS old; any workspace refresh covers it.
			let idOnlyWorkspaceRefreshPending = false;
			let idOnlyWorkspaceTimer: ReturnType<typeof setTimeout> | undefined;
			let lastWorkspaceRefreshAt = Number.NEGATIVE_INFINITY;
			let retryTimer: ReturnType<typeof setTimeout> | undefined;
			let source: EventSource | undefined;
			let sourceBaseUrl: string | undefined;
			// Base URL of a daemon that rejected ?after=latest (built before the cursor
			// existed). Its stream connects without one, replaying history as before.
			let cursorUnsupportedBaseUrl: string | undefined;
			const probeCursorSupport = (baseUrl: string, url: string) => {
				const controller = new AbortController();
				void fetch(url, { signal: controller.signal })
					.then((response) => {
						if (response.status === 400) cursorUnsupportedBaseUrl = baseUrl;
					})
					.catch(() => undefined)
					// A supporting daemon answers with an open stream; do not keep it.
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
				// Stream failures since it last delivered a frame, and the pending reopen.
				failures: number;
				reopenTimer?: ReturnType<typeof setTimeout>;
			};
			const remoteSources = new Map<string, RemoteConnection>();
			// Only a chat someone is looking at is read over the tunnel. A closed one
			// reads what changed when it opens again.
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
			// Do not repeatedly cancel a slow fetch under continuous CDC traffic. A
			// key receives at most one in-flight refresh and one queued catch-up.
			const refreshes = new Map<string, { dirty: boolean }>();
			const invalidate = (queryKey: QueryKey) => {
				if (disposed) return;
				const key = JSON.stringify(queryKey);
				const running = refreshes.get(key);
				if (running) {
					running.dirty = true;
					return;
				}
				// A fetch from polling/mounting may already predate this event. Wait
				// for it, then refresh once so joining its promise cannot lose the event.
				const state = { dirty: queryClient.isFetching({ queryKey, type: "active" }) > 0 };
				// An inactive Chat hover prefetch cannot refresh itself after this event.
				// Cancel it without interrupting unrelated imperative queries.
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
			// While the stream is down, poll what the stream would have refreshed.
			const pollRemote = (hostId: string) => {
				refreshRemote(hostId);
				invalidate(["reviewer-conversation", hostId]);
				refreshOpenRemoteChats(hostId);
			};
			// An id-only session update refreshes the host's lists at most once per
			// ID_ONLY_WORKSPACE_REFRESH_MS, as it does for the local daemon.
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
						// The same routing as the local stream: a conversation event refreshes
						// only that chat, and an id-only session update is rate limited.
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
									// A chat that is starting or waking leaves "connecting" on a
									// session update; a ready chat learns nothing from an id-only one.
									if (!idOnlySessionUpdate || !conversationSettled(queryClient, sessionId, hostId)) refreshOpenRemoteChat(hostId, sessionId);
								}
							} catch {
								// A malformed event still refreshes the host's lists below.
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
						// Only a live-page read clears a failed read's alert, and an idle chat
						// has no event to prompt one.
						refreshOpenRemoteChats(hostId);
					},
					() => {
						if (remoteSources.get(hostId) !== connection) return;
						if (connection.pollTimer === undefined) connection.pollTimer = setInterval(() => pollRemote(hostId), 2_000);
						refreshRemote(hostId);
						// A host that stays connected never rebuilds its stream, so without a
						// reopen one dropped link would leave it on the 2 s poll for good.
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
			// The scheduled flush body. Extracted so a leading-edge event can run
			// it immediately without waiting out a full window.
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
					// Catalogs are device-wide: pickers key them by agent, not by the
					// project the event names, so refresh every view of this agent
					// (pickers and Settings) together.
					invalidate(["agent-models", scope.agentId]);
				}
				pendingModelCatalogScopes.clear();
			};
			// A busy stream must not postpone visible updates until traffic
			// stops, and the first event after a quiet period must not wait out
			// a full window either. Flush on the leading edge when the last
			// flush is at least one window old; otherwise coalesce this and
			// later events into a single trailing flush. invalidate() still
			// dedups the resulting refetches per key, so the leading edge
			// cannot start a refetch storm.
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
			// Marks the workspace for the next flush once it is a full interval
			// old; until then a single timer waits for that moment.
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
					// A lifecycle refresh -- reconnect, daemon status change, base-URL change --
					// carries no event, so we cannot know which conversations moved. Normally the
					// replay that follows tells us, but when the event log has been truncated the
					// daemon starts us at head and no CDC arrives at all. EventSource cannot read
					// the header reporting that clamp, so refresh every conversation instead of
					// leaving an open chat frozen on its pre-gap snapshot.
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
						// The SSE endpoint sends the complete durable CDC event. Routing
						// fields such as sessionId live on that envelope, while trigger-built
						// details such as conversationId live inside its payload. Do not
						// mistake the payload for the entire event: doing so refreshes the
						// sidebar but leaves a Chat timeline frozen on its pre-turn snapshot.
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
						// A shell's program title changed; only the shell list shows it.
						if (typeof payload?.shellTerminalHandleId === "string") {
							void queryClient.invalidateQueries({ queryKey: shellTerminalsQueryKey });
							conversationOnly = true;
						}
						// A review run can start or finish without this window asking for
						// it: an agent ran `ao review trigger`, auto-review fired, or a
						// reviewer submitted. Refresh that session's reviews so the
						// inspector shows it instead of a stale verdict.
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
							// Async chat startup changes the session's provisionState, but
							// does not emit a conversationId, and the controller turning
							// ready writes nothing of its own: whichever session update
							// follows is what lets an open chat leave "connecting". Once the
							// chat is ready or working, an id-only update (usage bookkeeping,
							// cleanup facts, agent-switch rows) changes nothing in it, and
							// refetching for each one reloaded its whole page every few seconds.
							if (!idOnlySessionUpdate || !conversationSettled(queryClient, decoded.sessionId)) {
								pendingConversationSessions.add(decoded.sessionId);
							}
							// Native identity and provisioning changes carry the session-row
							// activity field. Recheck switch readiness even after its poll
							// budget expires, without reacting to id-only usage updates.
							if (typeof payload?.activity === "string") {
								pendingInterfaceTransitionSessions.add(decoded.sessionId);
							}
							pendingEditorHandoffSessions.add(decoded.sessionId);
						}
					} catch {
						// A malformed CDC payload still invalidates workspaces; it simply
						// cannot target a conversation cache precisely.
					}
				}
				if (idOnlySessionUpdate) queueIdOnlyWorkspaceRefresh();
				else if (!conversationOnly && !modelCatalogOnly) workspaceInvalidationPending = true;
				scheduleFlush();
			};

			// Consecutive scheduled rebuilds since the stream last opened. Paces
			// the retry so a daemon that keeps refusing the stream is not
			// hammered on a flat cadence (#4323).
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
				// EventSource is unavailable in jsdom (tests) and some preview surfaces; guard it.
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
				// Keep a still-usable source on the same base URL; replace one the
				// browser abandoned (CLOSED) or one bound to a stale port.
				if (source && sourceBaseUrl === baseUrl && source.readyState !== EVENTSOURCE_CLOSED) return;
				// A daemon that came back on a different port is a fresh target, not
				// a continuation of the dead one: do not make it serve the delay the
				// old port earned.
				if (sourceBaseUrl && sourceBaseUrl !== baseUrl) retries = 0;
				source?.close();
				source = undefined;
				sourceBaseUrl = baseUrl;
				try {
					// Start at the newest event. Without a cursor the daemon replays the
					// whole change_log (up to 100k rows under retention) on every connect,
					// although onopen already refreshes everything below. The daemon then
					// sends a cursor id, so the browser's own reconnect replays only the gap.
					// A daemon from before the cursor existed answers 400; that base URL
					// falls back to the cursorless stream (see probeCursorSupport).
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
						// Events emitted during the gap were lost; refetch once on (re)open.
						refreshWorkspaces();
					};
					source.onerror = () => {
						if (disposed || source !== connectedSource) return;
						// While readyState is CONNECTING the browser retries on its own;
						// either way the stream is not delivering, so surface it instead
						// of looping silently against a dead daemon.
						setEventsConnectionState("disconnected");
						if (source?.readyState === EVENTSOURCE_CLOSED) {
							// Closed before it ever opened: maybe this daemon rejects the cursor.
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
					source.onmessage = refreshWorkspaces; // unnamed events, if any
					for (const type of CDC_EVENT_TYPES) {
						source.addEventListener(type, refreshWorkspaces);
					}
					// EventSource auto-reconnects and resumes via Last-Event-ID while
					// CONNECTING; scheduleRetry only covers the terminal CLOSED state.
				} catch {
					source = undefined;
				}
			};

			// Main re-pushes the same status on every getStatus poll (~10s) when it is
			// attached to a daemon it did not spawn. Only a real change (state, port or
			// pid) can mean missed events; refreshing on each echo refetched every open
			// conversation and the workspace list every 10 seconds.
			let lastDaemonStatusKey: string | undefined;
			const removeDaemonListener = aoBridge.daemon.onStatus((status) => {
				const key = `${status?.state}:${status?.port ?? ""}:${status?.pid ?? ""}`;
				if (key === lastDaemonStatusKey) return;
				lastDaemonStatusKey = key;
				connectSource();
				refreshWorkspaces();
			});
			// Rebind when the daemon comes back on a different port, independent of
			// status-event ordering.
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
