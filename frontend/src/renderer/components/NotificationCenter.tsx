import { AppLink } from "./AppLink";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useParams } from "@tanstack/react-router";
import {
	ArrowUpRight,
	Bell,
	BellRing,
	CheckCheck,
	CircleAlert,
	GitMerge,
	GitPullRequestArrow,
	GitPullRequestClosed,
	Inbox,
	LoaderCircle,
	MessageSquareDot,
	RotateCcw,
	X,
} from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
	useClearAllNotificationsMutation,
	useClearNotificationMutation,
	useMarkAllNotificationsReadMutation,
	useNotificationsQuery,
} from "../hooks/useNotificationsQuery";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useRemoteWorkspaces, useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSummary } from "../types/workspace";
import { aoBridge } from "../lib/bridge";
import { apiErrorMessage } from "../lib/api-client";
import { formatTimeCompact } from "../lib/format-time";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST, parseRefKey, refKey, sessionUiKey, type HostId } from "../lib/hosts";
import {
	createNotificationsTransport,
	getCachedNotifications,
	getCachedUnreadCount,
	isNotificationsCacheFromClear,
	keepLatestNotificationsPage,
	type NotificationDTO,
	type NotificationsCache,
	type NotificationsPage,
	recentNotificationsQueryKey,
	unreadNotificationsQueryKey,
} from "../lib/notifications";
import { useUiStore } from "../stores/ui-store";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { captureRendererEvent } from "../lib/telemetry";
import { cn } from "../lib/utils";
import { TopbarButton } from "./TopbarButton";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { CloudNotificationList } from "./CloudNotificationList";
import { useCloudNotifications } from "../hooks/useCloudNotifications";
import { fetchRemoteNotificationsPage, remoteNotificationsQueryKey, useRemoteNotificationHosts } from "../hooks/useRemoteNotifications";

type NotificationCenterProps = {
	style?: React.CSSProperties;
};

const REMOTE_NOTIFICATION_PREFIX = "remote-notification:";
const notificationKey = (hostId: HostId, id: string) => refKey({ host: hostId, id });

function useNotificationTargetNavigation() {
	const navigateToSession = useNavigateToSession();
	const openSession = useCallback(
		(notification: NotificationDTO, hostId: HostId = LOCAL_HOST) => {
			const sessionId = notification.target.sessionId || notification.sessionId;
			if (!sessionId) return;
			void captureRendererEvent("ao.renderer.notification_opened", { target: "session" });
			navigateToSession(notification.projectId, sessionId, hostId);
		},
		[navigateToSession],
	);

	const openPrimary = useCallback(
		(notification: NotificationDTO, hostId: HostId = LOCAL_HOST) => {
			if (notification.target.kind === "pr" && notification.target.prUrl) {
				void captureRendererEvent("ao.renderer.notification_opened", { target: "pr" });
				window.open(notification.target.prUrl, "_blank", "noopener,noreferrer");
				return;
			}
			openSession(notification, hostId);
		},
		[openSession],
	);

	return { openPrimary, openSession };
}

function useSessionTerminationLookup(
	workspaces: WorkspaceSummary[] | undefined,
	isError: boolean,
	isSuccess: boolean,
	refetch: () => void,
): {
	retryWorkspace: () => void;
	sessionsReady: boolean;
	terminatedIds: Set<string>;
	workspaceError: boolean;
} {
	const terminatedIds = useMemo(() => {
		const ids = new Set<string>();
		for (const workspace of workspaces ?? []) {
			for (const session of workspace.sessions) {
				if (session.isTerminated === true || session.status === "terminated") {
					ids.add(sessionUiKey(session.id, workspace.hostId));
				}
			}
		}
		return ids;
	}, [workspaces]);
	// Only successful workspace data is trustworthy. Pending and error both leave
	// sessions non-navigable — a failed query must not treat terminated rows as live.
	return {
		retryWorkspace: refetch,
		sessionsReady: isSuccess,
		terminatedIds,
		workspaceError: isError,
	};
}

/**
 * Radix only mounts PopoverContent while the bell is open. Keeping the broad
 * workspace observer here means streamed workspace activity cannot wake the
 * always-mounted bell while its panel is closed.
 */
function NotificationWorkspaceState({
	children,
}: {
	children: (state: {
		retryWorkspace: () => void;
		loadedRemoteSessionIds: Set<string>;
		sessionMeta: Map<string, { projectName: string; sessionName: string }>;
		sessionsReady: boolean;
		terminatedIds: Set<string>;
		workspaceError: boolean;
	}) => ReactNode;
}) {
	const workspaceQuery = useWorkspaceQuery();
	const remoteWorkspaces = useRemoteWorkspaces();
	const retryWorkspace = useCallback(() => {
		void workspaceQuery.refetch();
		void remoteWorkspaces.refetch();
	}, [remoteWorkspaces.refetch, workspaceQuery.refetch]);
	const { sessionsReady, terminatedIds, workspaceError } = useSessionTerminationLookup(
		[...(workspaceQuery.data ?? []), ...remoteWorkspaces.data],
		workspaceQuery.isError,
		workspaceQuery.isSuccess,
		retryWorkspace,
	);
	const sessionMeta = useMemo(() => {
		const map = new Map<string, { projectName: string; sessionName: string }>();
		for (const workspace of workspaceQuery.data ?? []) {
			for (const session of workspace.sessions) {
				map.set(sessionUiKey(session.id, workspace.hostId), { projectName: workspace.name, sessionName: session.title });
			}
		}
		for (const workspace of remoteWorkspaces.data) {
			for (const session of workspace.sessions) {
				map.set(sessionUiKey(session.id, workspace.hostId), { projectName: workspace.name, sessionName: session.title });
			}
		}
		return map;
	}, [remoteWorkspaces.data, workspaceQuery.data]);
	const loadedRemoteSessionIds = useMemo(() => {
		const ids = new Set<string>();
		for (const workspace of remoteWorkspaces.data) {
			if (!workspace.hostId || !remoteWorkspaces.loadedSessionHostIds.includes(workspace.hostId) || remoteWorkspaces.failedHostIds.includes(workspace.hostId)) continue;
			for (const session of workspace.sessions) {
				ids.add(sessionUiKey(session.id, workspace.hostId));
			}
		}
		return ids;
	}, [remoteWorkspaces.data, remoteWorkspaces.failedHostIds, remoteWorkspaces.loadedSessionHostIds]);
	return <>{children({ retryWorkspace, loadedRemoteSessionIds, sessionMeta, sessionsReady, terminatedIds, workspaceError: workspaceError || remoteWorkspaces.failedHostIds.length > 0 })}</>;
}

export function NotificationRuntime() {
	const queryClient = useQueryClient();
	const { openPrimary } = useNotificationTargetNavigation();
	const unreadQuery = useNotificationsQuery("unread");
	const remoteUnread = useRemoteNotificationHosts("unread");
	const unreadCount = getCachedUnreadCount(unreadQuery.data) + remoteUnread.totalUnreadCount;
	const params = useParams({ strict: false }) as { hostId?: string; sessionId?: string };
	const routeRef = useRef(params);
	routeRef.current = params;
	const seenRemoteIds = useRef(new Map<HostId, Set<string>>());
	const shownRemoteNotifications = useRef(new Map<string, NotificationDTO>());

	// Being on the session route is not the same as watching the agent: its pane
	// renders one terminal at a time, so a shell or reviewer tab hides the agent
	// while the route is unchanged. Only report the session whose agent terminal
	// is the one on screen. Read the store imperatively — this feeds a getter for
	// the long-lived SSE connection, which needs the current value, not a render.
	const getVisibleAgentSessionId = useCallback(() => {
		const { hostId, sessionId } = routeRef.current;
		if (!sessionId || hostId) return undefined;
		return useUiStore.getState().visibleTerminalKindBySession[sessionId] === "worker" ? sessionId : undefined;
	}, []);

	useEffect(
		() => createNotificationsTransport(queryClient, getVisibleAgentSessionId).connect(),
		[getVisibleAgentSessionId, queryClient],
	);

	// Keep the OS launcher badge in sync here rather than in NotificationCenter:
	// NotificationRuntime is always mounted in the shell, whereas the notification
	// bell is absent from the Linux topbar and only mounts on the sessions board.
	useEffect(() => {
		void aoBridge.notifications.setBadge(unreadCount);
	}, [unreadCount]);

	useEffect(() => {
		for (const host of remoteUnread.hosts) {
			if (!host.data) continue;
			const currentIds = new Set(host.data.notifications.map((item) => item.id));
			const previousIds = seenRemoteIds.current.get(host.hostId);
			seenRemoteIds.current.set(host.hostId, currentIds);
			if (!previousIds) continue; // Reconnecting to a host must not replay its existing inbox.
			for (const notification of host.data.notifications) {
				if (previousIds.has(notification.id)) continue;
				const id = `${REMOTE_NOTIFICATION_PREFIX}${notificationKey(host.hostId, notification.id)}`;
				shownRemoteNotifications.current.set(id, notification);
				if (shownRemoteNotifications.current.size > 256) {
					shownRemoteNotifications.current.delete(shownRemoteNotifications.current.keys().next().value!);
				}
				const { hostId, sessionId } = routeRef.current;
				const watched = notification.type === "needs_input" && hostId === host.hostId &&
					sessionId === notification.sessionId && document.visibilityState === "visible" && document.hasFocus() &&
					useUiStore.getState().visibleTerminalKindBySession[sessionUiKey(sessionId, hostId)] === "worker";
				void aoBridge.notifications.show({
					id,
					title: `${host.label}: ${notification.title}`,
					body: notification.body || undefined,
					type: notification.type,
					watched,
				});
			}
		}
	}, [remoteUnread.hosts]);

	useEffect(() => {
		return aoBridge.notifications.onClick((id) => {
			if (id.startsWith(REMOTE_NOTIFICATION_PREFIX)) {
				try {
					const { host, id: notificationId } = parseRefKey(id.slice(REMOTE_NOTIFICATION_PREFIX.length));
					const unread = queryClient.getQueryData<NotificationsPage>(remoteNotificationsQueryKey(host, "unread"));
					const all = queryClient.getQueryData<NotificationsPage>(remoteNotificationsQueryKey(host, "all"));
					const notification = shownRemoteNotifications.current.get(id) ?? [...(unread?.notifications ?? []), ...(all?.notifications ?? [])].find((item) => item.id === notificationId);
					if (notification) openPrimary(notification, host);
				} catch { /* Ignore malformed or obsolete OS notification IDs. */ }
				return;
			}
			const unread = queryClient.getQueryData<NotificationsCache>(unreadNotificationsQueryKey);
			const recent = queryClient.getQueryData<NotificationsCache>(recentNotificationsQueryKey);
			const notification = [...getCachedNotifications(unread), ...getCachedNotifications(recent)].find(
				(item) => item.id === id,
			);
			if (notification) openPrimary(notification);
		});
	}, [openPrimary, queryClient]);

	return null;
}

export function NotificationCenter({ style }: NotificationCenterProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [actionError, setActionError] = useState<string | null>(null);
	const [markReadError, setMarkReadError] = useState<string | null>(null);
	// Bumped by the mark-read Retry control so a failed ack can run again even
	// when visibleUnreadKey is unchanged (removing ids from the ref alone does not).
	const [ackRetryNonce, setAckRetryNonce] = useState(0);
	const [open, setOpen] = useState(false);
	// Opening marks unread as read, which would drop the highlight under the
	// cursor. Keep the open-time unread ids highlighted until the panel closes.
	const [highlightedIds, setHighlightedIds] = useState<Set<string>>(() => new Set());
	const [clearingNotificationIds, setClearingNotificationIds] = useState<Set<string>>(() => new Set());
	const [restoringSessionId, setRestoringSessionId] = useState<string | undefined>();
	const [clearingAll, setClearingAll] = useState(false);
	const [remoteOlderPages, setRemoteOlderPages] = useState<Record<string, NotificationsPage[]>>({});
	const [remoteLoadingEarlierHost, setRemoteLoadingEarlierHost] = useState<string | null>(null);
	const [remoteEarlierError, setRemoteEarlierError] = useState<string | null>(null);
	const remoteHistoryGeneration = useRef(0);
	const remoteFirstPageKeys = useRef(new Map<string, string>());
	const unreadQuery = useNotificationsQuery("unread");
	const allQuery = useNotificationsQuery("all", open);
	const remoteUnread = useRemoteNotificationHosts("unread");
	const remoteAll = useRemoteNotificationHosts("all", open);
	const cloudUnreadQuery = useCloudNotifications("unread");
	const markAllRead = useMarkAllNotificationsReadMutation();
	const clearAll = useClearAllNotificationsMutation();
	const clearOne = useClearNotificationMutation();
	const restoreSession = useRestoreSession();
	const notifications = useMemo(() => getCachedNotifications(allQuery.data), [allQuery.data]);
	const remoteItems = useMemo(() => remoteAll.hosts.map((host) => {
		const seen = new Set<string>();
		const unread = remoteUnread.hosts.find((item) => item.hostId === host.hostId)?.data;
		const items = [unread, host.data, ...(remoteOlderPages[host.hostId] ?? [])].filter((page): page is NotificationsPage => Boolean(page)).flatMap((page) =>
			page.notifications.filter((notification) => {
				if (seen.has(notification.id)) return false;
				seen.add(notification.id);
				return true;
			}),
		);
		return { ...host, notifications: items };
	}), [remoteAll.hosts, remoteOlderPages, remoteUnread.hosts]);
	useEffect(() => {
		for (const host of remoteAll.hosts) {
			if (!host.data) continue;
			const key = JSON.stringify([host.data.notifications.map((item) => item.id), host.data.nextCursor]);
			const previous = remoteFirstPageKeys.current.get(host.hostId);
			remoteFirstPageKeys.current.set(host.hostId, key);
			if (previous === undefined || previous === key) continue;
			if (!(remoteOlderPages[host.hostId]?.length) && remoteLoadingEarlierHost !== host.hostId) continue;
			// Cursor pagination shifts when the first page changes; reload older pages from its new boundary.
			remoteHistoryGeneration.current += 1;
			setRemoteOlderPages((current) => ({ ...current, [host.hostId]: [] }));
			setRemoteLoadingEarlierHost(null);
		}
	}, [remoteAll.hosts, remoteLoadingEarlierHost, remoteOlderPages]);
	const entries = useMemo(() => [
		...notifications.map((notification) => ({ hostId: LOCAL_HOST, label: "", notification })),
		...remoteItems.flatMap((host) => host.notifications.map((notification) => ({
			hostId: host.hostId, label: host.label, notification,
		}))),
	].sort((a, b) => b.notification.createdAt.localeCompare(a.notification.createdAt)), [notifications, remoteItems]);
	const unreadCount = getCachedUnreadCount(unreadQuery.data) + remoteUnread.totalUnreadCount + (cloudUnreadQuery.data?.unreadCount ?? 0);
	const confirmedClearSnapshot = isNotificationsCacheFromClear(queryClient);
	const { openSession } = useNotificationTargetNavigation();
	const markAllMutate = markAllRead.mutateAsync;

	// Concrete ids only — never `[]` — so unread pages past the first stay
	// reachable and arrive as unread (still highlighted) when the all-list loads them.
	const acknowledgedIdsRef = useRef<Set<string>>(new Set());
	const acknowledgedRemoteIdsRef = useRef<Set<string>>(new Set());
	const remoteHostsRef = useRef(remoteItems);
	remoteHostsRef.current = remoteItems;
	const visibleRemoteUnreadKey = remoteItems.flatMap((host) =>
		host.notifications.filter((item) => item.status === "unread")
			.map((item) => notificationKey(host.hostId, item.id)),
	).join("|");
	const visibleUnreadIds = useMemo(() => {
		const ids = new Set<string>();
		for (const item of getCachedNotifications(unreadQuery.data)) {
			if (item.status === "unread") ids.add(item.id);
		}
		for (const item of notifications) {
			if (item.status === "unread") ids.add(item.id);
		}
		return [...ids];
	}, [notifications, unreadQuery.data]);
	const visibleUnreadKey = visibleUnreadIds.join("|");

	// Opening the panel acknowledges what has actually been loaded. Later unread
	// rows are acknowledged as they appear, keeping the unread pagination cursor.
	useEffect(() => {
		if (!open) {
			setHighlightedIds(new Set());
			setMarkReadError(null);
			acknowledgedIdsRef.current = new Set();
			return;
		}
		if (unreadQuery.isLoading) return;

		const visibleIds = visibleUnreadKey === "" ? [] : visibleUnreadKey.split("|");
		const newly = visibleIds.filter((id) => !acknowledgedIdsRef.current.has(id));
		if (newly.length === 0) return;

		for (const id of newly) acknowledgedIdsRef.current.add(id);
		setHighlightedIds((current) => {
			const next = new Set(current);
			let changed = false;
			for (const id of newly) {
				const key = notificationKey(LOCAL_HOST, id);
				if (next.has(key)) continue;
				next.add(key);
				changed = true;
			}
			return changed ? next : current;
		});

		setMarkReadError(null);
		void captureRendererEvent("ao.renderer.notification_mark_read_requested", { scope: "all" });
		void markAllMutate(newly)
			.then(() => captureRendererEvent("ao.renderer.notification_mark_read_succeeded", { scope: "all" }))
			.catch((error: unknown) => {
				void captureRendererEvent("ao.renderer.notification_mark_read_failed", { scope: "all" });
				for (const id of newly) acknowledgedIdsRef.current.delete(id);
				setMarkReadError(error instanceof Error ? error.message : t("notify.couldNotMarkAllRead"));
			});
	}, [ackRetryNonce, markAllMutate, open, t, unreadQuery.isLoading, visibleUnreadKey]);

	useEffect(() => {
		if (!open) {
			acknowledgedRemoteIdsRef.current.clear();
			return;
		}
		for (const host of remoteHostsRef.current) {
			if (host.notifications.length === 0) continue;
			const unreadIds = host.notifications
				.filter((item) => item.status === "unread")
				.map((item) => item.id)
				.filter((id) => !acknowledgedRemoteIdsRef.current.has(notificationKey(host.hostId, id)));
			if (unreadIds.length === 0) continue;
			for (const id of unreadIds) acknowledgedRemoteIdsRef.current.add(notificationKey(host.hostId, id));
			setHighlightedIds((current) => {
				const next = new Set(current);
				for (const id of unreadIds) next.add(notificationKey(host.hostId, id));
				return next;
			});
			void (async () => {
				try {
					const { error } = await clientForHost(host.hostId).POST("/api/v1/notifications/read-all", { body: { ids: unreadIds } });
					if (error) throw new Error(apiErrorMessage(error, "Could not mark notifications read"));
					await queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(host.hostId, "unread") });
					await queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(host.hostId, "all") });
				} catch (error) {
					for (const id of unreadIds) acknowledgedRemoteIdsRef.current.delete(notificationKey(host.hostId, id));
					setMarkReadError(`${host.label}: ${error instanceof Error ? error.message : t("notify.couldNotMarkAllRead")}`);
				}
			})();
		}
	}, [ackRetryNonce, open, queryClient, t, visibleRemoteUnreadKey]);

	const setPanelOpen = useCallback((nextOpen: boolean) => {
		setOpen(nextOpen);
		if (!nextOpen) {
			remoteHistoryGeneration.current += 1;
			remoteFirstPageKeys.current.clear();
			setRemoteOlderPages({});
			setRemoteLoadingEarlierHost(null);
			setRemoteEarlierError(null);
			keepLatestNotificationsPage(queryClient, unreadNotificationsQueryKey);
			keepLatestNotificationsPage(queryClient, recentNotificationsQueryKey);
		}
	}, [queryClient]);

	const retryMarkRead = () => {
		setMarkReadError(null);
		setAckRetryNonce((nonce) => nonce + 1);
	};

	const openSessionAndDismiss = useCallback((notification: NotificationDTO, hostId: HostId) => {
		openSession(notification, hostId);
		setPanelOpen(false);
	}, [openSession, setPanelOpen]);

	const restoreAndOpen = useCallback(async (notification: NotificationDTO, hostId: HostId) => {
		const sessionId = notification.target.sessionId || notification.sessionId;
		if (!sessionId || restoringSessionId) return;
		setRestoringSessionId(sessionUiKey(sessionId, hostId));
		setActionError(null);
		try {
			const result = hostId === LOCAL_HOST
				? await restoreSession(sessionId)
				: await restoreSession(sessionId, hostId);
			if (result.status === "success") {
				openSession(notification, hostId);
				setPanelOpen(false);
				return;
			}
			setActionError(result.status === "not_resumable" ? t("notify.restoreUnavailable") : result.message);
		} finally {
			setRestoringSessionId(undefined);
		}
	}, [openSession, restoreSession, restoringSessionId, setPanelOpen, t]);

	const handleClearAll = useCallback(() => {
		if (clearingAll) return;
		setActionError(null);
		setClearingAll(true);
		remoteHistoryGeneration.current += 1;
		setRemoteOlderPages({});
		setRemoteLoadingEarlierHost(null);
		setRemoteEarlierError(null);
		void Promise.allSettled([
			clearAll.mutateAsync(),
			...remoteAll.hosts.map(async (host) => {
				const { error } = await clientForHost(host.hostId).DELETE("/api/v1/notifications");
				if (error) throw new Error(`${host.label}: ${apiErrorMessage(error, t("notify.couldNotClearAll"))}`);
				setRemoteOlderPages((current) => ({ ...current, [host.hostId]: [] }));
				await queryClient.invalidateQueries({ queryKey: ["remote-notifications", host.hostId] });
			}),
		]).then((results) => {
			const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
			if (failure) setActionError(failure.reason instanceof Error ? failure.reason.message : t("notify.couldNotClearAll"));
		}).finally(() => setClearingAll(false));
	}, [clearAll, clearingAll, queryClient, remoteAll.hosts, t]);

	const handleClear = useCallback((notification: NotificationDTO, hostId: HostId) => {
		setActionError(null);
		const key = notificationKey(hostId, notification.id);
		setClearingNotificationIds((current) => new Set(current).add(key));
		void (hostId === LOCAL_HOST ? clearOne.mutateAsync(notification) : (async () => {
			const { error, response } = await clientForHost(hostId).DELETE("/api/v1/notifications/{id}", {
				params: { path: { id: notification.id } },
			});
			if (error && response.status !== 404) throw new Error(apiErrorMessage(error, t("notify.couldNotClearOne")));
			remoteHistoryGeneration.current += 1;
			setRemoteLoadingEarlierHost(null);
			setRemoteOlderPages((current) => ({
				...current,
				[hostId]: (current[hostId] ?? []).map((page) => ({
					...page, notifications: page.notifications.filter((item) => item.id !== notification.id),
				})),
			}));
			await queryClient.invalidateQueries({ queryKey: ["remote-notifications", hostId] });
		})())
			.catch((error: unknown) => {
				setActionError(error instanceof Error ? error.message : t("notify.couldNotClearOne"));
			})
			.finally(() => {
				setClearingNotificationIds((current) => {
					const next = new Set(current);
					next.delete(key);
					return next;
				});
			});
	}, [clearOne, queryClient, t]);

	const loadEarlierOnScroll = (event: React.UIEvent<HTMLDivElement>) => {
		const list = event.currentTarget;
		const remaining = list.scrollHeight - list.scrollTop - list.clientHeight;
		if (remaining > 80 || !allQuery.hasNextPage || allQuery.isFetchingNextPage) return;
		void allQuery.fetchNextPage();
	};
	const loadRemoteEarlier = async (hostId: string, cursor: string) => {
		if (remoteLoadingEarlierHost) return;
		const generation = remoteHistoryGeneration.current;
		setRemoteLoadingEarlierHost(hostId);
		setRemoteEarlierError(null);
		try {
			const page = await fetchRemoteNotificationsPage(hostId, "all", cursor);
			if (generation === remoteHistoryGeneration.current) {
				setRemoteOlderPages((current) => ({ ...current, [hostId]: [...(current[hostId] ?? []), page] }));
			}
		} catch (error) {
			if (generation === remoteHistoryGeneration.current) {
				setRemoteEarlierError(error instanceof Error ? error.message : t("notify.earlierLoadFailed"));
			}
		} finally {
			if (generation === remoteHistoryGeneration.current) setRemoteLoadingEarlierHost(null);
		}
	};

	const isEmpty = entries.length === 0;

	return (
		<Popover onOpenChange={setPanelOpen} open={open}>
			<PopoverTrigger asChild>
				<TopbarButton
					aria-label={unreadCount > 0 ? t("notify.unreadCount", { count: unreadCount }) : t("notify.bell")}
					className="relative"
					style={style}
					variant="icon"
				>
					{unreadCount > 0 ? (
						<BellRing className="size-icon-base fill-current text-foreground" aria-hidden="true" />
					) : (
						<Bell className="size-icon-base" aria-hidden="true" />
					)}
					{unreadCount > 0 ? (
						<span className="pointer-events-none absolute right-px top-px grid h-3 min-w-3 place-items-center rounded-full bg-accent-strong px-0.5 font-mono text-[7px] font-semibold leading-none text-accent-foreground shadow-sm ring-1 ring-background">
							{unreadCount > 99 ? "99+" : unreadCount}
						</span>
					) : null}
				</TopbarButton>
			</PopoverTrigger>
			<PopoverContent
				align="end"
				aria-label={t("notify.title")}
				className="w-notification-width max-w-[calc(100vw-1rem)] overflow-hidden rounded-panel border-border-strong p-0 shadow-xl"
				sideOffset={8}
			>
				<div className="flex items-center justify-between gap-2 border-b border-border bg-[var(--color-overlay-subtle)] px-4 py-3.5">
					<p className="text-subtitle font-semibold tracking-tight text-foreground">{t("notify.title")}</p>
					<button
						className="shrink-0 text-caption font-medium text-muted-foreground transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
						disabled={isEmpty || clearingAll}
						onClick={handleClearAll}
						type="button"
					>
						{t("notify.clearAll")}
					</button>
				</div>
				<NotificationWorkspaceState>
					{({ retryWorkspace, loadedRemoteSessionIds, sessionMeta, sessionsReady, terminatedIds, workspaceError }) => (
						<>
							{markReadError ? (
					<div
						aria-live="polite"
						className="flex items-center justify-between gap-2 border-b border-border bg-error/5 px-4 py-2 text-caption text-error"
					>
						<span>{markReadError}</span>
						<button
							className="shrink-0 font-medium underline underline-offset-2 hover:text-foreground"
							onClick={retryMarkRead}
							type="button"
						>
							{t("notify.retry")}
						</button>
					</div>
				) : null}
				{actionError ? (
					<div className="border-b border-border bg-error/5 px-4 py-2 text-caption text-error">{actionError}</div>
				) : null}
				{workspaceError ? (
					<div
						aria-live="polite"
						className="flex items-center justify-between gap-2 border-b border-border bg-error/5 px-4 py-2 text-caption text-error"
					>
						<span>{t("notify.workspaceLoadFailed")}</span>
						<button
							className="shrink-0 font-medium underline underline-offset-2 hover:text-foreground"
							onClick={retryWorkspace}
							type="button"
						>
							{t("notify.retry")}
						</button>
					</div>
				) : null}
						{(allQuery.isError || remoteAll.hosts.some((host) => host.isError)) && isEmpty && !confirmedClearSnapshot ? (
							<NotificationEmpty icon={CircleAlert} message={t("notify.loadFailed")} />
						) : (allQuery.isLoading || remoteAll.hosts.some((host) => host.isLoading)) && isEmpty ? (
					<NotificationEmpty icon={Inbox} message={t("notify.loading")} />
				) : isEmpty ? (
					<NotificationEmpty icon={CheckCheck} message={t("notify.emptyAll")} />
					) : (
						<>
						<div
						aria-busy={allQuery.isFetchingNextPage || remoteLoadingEarlierHost !== null}
						className="board-scrollbar max-h-notification-max-height overflow-y-auto overscroll-contain py-1.5"
						onScroll={loadEarlierOnScroll}
						role="list"
					>
						{entries.map(({ hostId, label, notification }) => {
							const sessionId = notification.target.sessionId || notification.sessionId;
							const sessionKey = sessionId ? sessionUiKey(sessionId, hostId) : undefined;
							const sessionReady = hostId === LOCAL_HOST ? sessionsReady : Boolean(sessionKey && loadedRemoteSessionIds.has(sessionKey));
							const meta = sessionKey ? sessionMeta.get(sessionKey) : undefined;
							const terminated = Boolean(sessionKey) && terminatedIds.has(sessionKey ?? "");
							const key = notificationKey(hostId, notification.id);
							// Restoring only makes sense when an agent is actually paused waiting
							// on input. PR outcomes (ready_to_merge, pr_merged, pr_closed_unmerged)
							// describe work that already finished — there is nothing to resume, so
							// a terminated session behind one of these should stay viewable, not
							// gated behind a restore action.
							const offerRestore = sessionReady && terminated && notification.type === "needs_input";
							return (
								<NotificationItem
									highlighted={highlightedIds.has(key) || notification.status === "unread"}
									key={key}
									notification={notification}
									onOpenSession={() => openSessionAndDismiss(notification, hostId)}
									onRestore={() => restoreAndOpen(notification, hostId)}
									onClear={() => handleClear(notification, hostId)}
									clearing={clearingNotificationIds.has(key)}
									clearDisabled={clearingNotificationIds.has(key) || clearingAll}
									restoring={restoringSessionId === sessionKey}
									restoreDisabled={restoringSessionId !== undefined}
									hostLabel={label}
									projectName={meta?.projectName}
									sessionName={meta?.sessionName}
									sessionsReady={sessionReady}
									terminated={terminated}
									offerRestore={offerRestore}
								/>
							);
						})}
						{allQuery.isFetchNextPageError ? (
							<div
								aria-live="polite"
								className="flex items-center justify-center gap-2 px-4 py-3 text-caption text-error"
							>
								{t("notify.earlierLoadFailed")}
								<button
									className="font-medium underline underline-offset-2 hover:text-foreground"
									onClick={() => void allQuery.fetchNextPage()}
									type="button"
								>
									{t("notify.retry")}
								</button>
							</div>
						) : allQuery.isFetchingNextPage ? (
							<div
								aria-live="polite"
								className="flex items-center justify-center gap-2 px-4 py-3 text-caption text-passive"
							>
								<LoaderCircle className="size-icon-md animate-spin" aria-hidden="true" />
								{t("notify.loadingEarlier")}
							</div>
						) : null}
						{remoteAll.hosts.map((host) => {
							const older = remoteOlderPages[host.hostId] ?? [];
							const cursor = older.length > 0 ? older[older.length - 1].nextCursor : host.data?.nextCursor;
							if (!cursor || !host.data) return null;
							return (
								<button
									className="block w-full px-4 py-2 text-center text-caption font-medium text-muted-foreground hover:text-foreground disabled:opacity-50"
									disabled={remoteLoadingEarlierHost !== null}
									key={host.hostId}
									onClick={() => void loadRemoteEarlier(host.hostId, cursor)}
									type="button"
								>
									{remoteLoadingEarlierHost === host.hostId ? t("notify.loadingEarlier") : t("notify.loadEarlierFromHost", { host: host.label })}
								</button>
							);
						})}
						{remoteEarlierError ? <div aria-live="polite" className="px-4 py-2 text-caption text-error">{remoteEarlierError}</div> : null}
					</div>
						</>
				)}
						</>
					)}
				</NotificationWorkspaceState>
				<CloudNotificationList />
			</PopoverContent>
		</Popover>
	);
}

function NotificationEmpty({ icon: Icon, message }: { icon: typeof Bell; message: string }) {
	return (
		<div className="flex flex-col items-center gap-2.5 px-4 py-5 text-center">
			<div className="grid size-control-xl place-items-center rounded-full border border-border bg-surface text-passive">
				<Icon className={cn("size-icon-base", Icon === LoaderCircle && "animate-spin")} aria-hidden="true" />
			</div>
			<p className="text-control text-muted-foreground">{message}</p>
		</div>
	);
}

/**
 * The whole row is the click target for live sessions. A terminated session
 * behind a `needs_input` notification is not navigable — restore is the only
 * action, since there is a paused agent to resume. PR-outcome notifications
 * (`offerRestore` false) describe finished work, so a terminated session stays
 * viewable there instead of being gated behind restore. PR titles stay a real
 * link so a PR row can open the PR without a separate icon button.
 */
const NotificationItem = memo(function NotificationItem({
	highlighted,
	notification,
	offerRestore,
	onOpenSession,
	onRestore,
	onClear,
	hostLabel,
	projectName,
	clearing,
	clearDisabled,
	restoring,
	restoreDisabled,
	sessionName,
	sessionsReady,
	terminated,
}: {
	highlighted: boolean;
	notification: NotificationDTO;
	offerRestore: boolean;
	onOpenSession: (notification: NotificationDTO) => void;
	onRestore: (notification: NotificationDTO) => void;
	onClear: (notification: NotificationDTO) => void;
	hostLabel?: string;
	projectName?: string;
	clearing: boolean;
	clearDisabled: boolean;
	restoring: boolean;
	restoreDisabled: boolean;
	sessionName?: string;
	sessionsReady: boolean;
	terminated: boolean;
}) {
	const { t } = useTranslation();
	const Icon = notificationIcon(notification.type);
	const sessionId = notification.target.sessionId || notification.sessionId;
	const canOpenSession = Boolean(sessionId) && sessionsReady && (!terminated || !offerRestore);
	const copy = notificationCopy(notification, sessionName);
	const titleLink = notificationPRTitleLink(notification, copy.title);
	const showSessionMeta = Boolean(sessionName) && !notificationMentions(copy, sessionName ?? "");
	const openRow = () => {
		if (canOpenSession) onOpenSession(notification);
	};
	return (
		<div role="listitem">
			<div
				className={cn(
					"group grid grid-cols-notification items-start gap-3 px-4 py-3 text-left transition-[background-color,opacity,transform] duration-fast will-change-transform",
					highlighted && "notification-row-enter",
					canOpenSession
						? "cursor-pointer hover:bg-interactive-hover active:scale-[0.99] active:bg-interactive-active"
						: "cursor-default",
					!highlighted && "opacity-55 hover:opacity-80",
				)}
				onClick={openRow}
				onKeyDown={(event) => {
					if (!canOpenSession) return;
					if (event.key !== "Enter" && event.key !== " ") return;
					event.preventDefault();
					openRow();
				}}
				role={canOpenSession ? "button" : undefined}
				tabIndex={canOpenSession ? 0 : undefined}
				title={canOpenSession ? t("notify.openSessionTitle") : undefined}
			>
				<div
					className={cn(
						"grid size-notification-icon shrink-0 place-items-center transition-transform duration-fast group-hover:brightness-110 group-active:scale-90",
						notificationIconClass(notification.type),
					)}
				>
					<Icon className="size-5" strokeWidth={2} aria-hidden="true" />
				</div>
				<div className="min-w-0">
					{/* Match the 26px icon band so the title centers with the left glyph. */}
					<div className="flex min-h-notification-icon items-center">
						<span
							aria-label={copy.title}
							className={cn(
								"min-w-0 break-words text-control leading-snug text-foreground",
								highlighted && "font-medium",
							)}
						>
							{titleLink ? (
								<>
									{titleLink.before}
									<AppLink
										aria-label={t("inspector.openPR", { number: titleLink.number })}
										className="inline-flex items-center gap-0.5 underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
										href={titleLink.url}
										onClick={(event) => {
											event.stopPropagation();
											void captureRendererEvent("ao.renderer.notification_opened", { target: "pr" });
										}}
										rel="noopener noreferrer"
										target="_blank"
									>
										{titleLink.label}
										<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />
									</AppLink>
									{titleLink.after}
								</>
							) : (
								copy.title
							)}
						</span>
					</div>
					{copy.body ? (
						<p className="mt-0.5 whitespace-pre-wrap break-words text-caption leading-snug text-muted-foreground">
							{copy.body}
						</p>
					) : null}
					{hostLabel || projectName || showSessionMeta ? (
						<p className="mt-1 flex min-w-0 items-center gap-1.5 text-caption leading-none text-passive">
							{hostLabel ? <span className="shrink-0 font-medium text-muted-foreground">{hostLabel}</span> : null}
							{hostLabel && (projectName || showSessionMeta) ? <span aria-hidden="true">·</span> : null}
							{projectName ? (
								<span className="truncate font-medium text-muted-foreground">{projectName}</span>
							) : null}
							{projectName && showSessionMeta ? <span aria-hidden="true">·</span> : null}
							{showSessionMeta ? <span className="truncate">{sessionName}</span> : null}
						</p>
					) : null}
				</div>
				{/* Time and row actions share the same icon-height band. */}
				<div className="flex h-notification-icon shrink-0 items-center gap-1">
					<time className="shrink-0 font-mono text-[9px] leading-none text-passive" dateTime={notification.createdAt}>
						{formatTimeCompact(notification.createdAt)}
					</time>
					{offerRestore && sessionId ? (
						<Tooltip delayDuration={0}>
							<TooltipTrigger asChild>
								<button
									aria-label={t("shell.restoreSession")}
									className="grid size-notification-icon place-items-center rounded-md text-passive transition-colors hover:bg-interactive-active hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
									disabled={restoreDisabled}
									onClick={(event) => {
										event.stopPropagation();
										onRestore(notification);
									}}
									type="button"
								>
									<RotateCcw className={cn("size-icon-md", restoring && "animate-spin")} aria-hidden="true" />
								</button>
							</TooltipTrigger>
							<TooltipContent side="top">
								{restoring ? t("shell.restoringSession") : t("shell.restoreSession")}
							</TooltipContent>
						</Tooltip>
					) : null}
					<Tooltip delayDuration={0}>
						<TooltipTrigger asChild>
							<button
								aria-label={t("notify.clearOne", { title: hostLabel ? `${hostLabel}: ${copy.title}` : copy.title })}
								className="grid size-notification-icon place-items-center rounded-md text-passive transition-colors hover:bg-interactive-active hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
								disabled={clearDisabled}
								onClick={(event) => {
									event.stopPropagation();
									onClear(notification);
								}}
								onKeyDown={(event) => event.stopPropagation()}
								type="button"
							>
								{clearing ? (
									<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
								) : (
									<X className="size-icon-sm" aria-hidden="true" />
								)}
							</button>
						</TooltipTrigger>
						<TooltipContent side="top">{t("notify.clearOneShort")}</TooltipContent>
					</Tooltip>
				</div>
			</div>
		</div>
	);
});

type NotificationCopy = Pick<NotificationDTO, "body" | "title">;

function notificationPRTitleLink(
	notification: NotificationDTO,
	title: string,
): { after: string; before: string; label: string; number: string; url: string } | null {
	const url = notification.target.kind === "pr" ? notification.target.prUrl : notification.prUrl;
	if (!url) return null;
	const match = /\bPR\s*#(\d+)\b/i.exec(title);
	if (!match || match.index === undefined) return null;
	return {
		before: title.slice(0, match.index),
		label: match[0],
		number: match[1],
		after: title.slice(match.index + match[0].length),
		url,
	};
}

function notificationCopy(notification: NotificationDTO, sessionName?: string): NotificationCopy {
	if (notification.type !== "ready_to_merge") {
		return { title: notification.title, body: notification.body };
	}

	const session = sessionName?.trim();
	if (!session) {
		return { title: notification.title, body: notification.body };
	}

	const legacySessionTitle = `${session} is ready to merge`;
	const title =
		notification.title.trim().toLocaleLowerCase() === legacySessionTitle.toLocaleLowerCase()
			? readyNotificationFallbackTitle(notification)
			: notification.title;

	return {
		title,
		body: `PR from session ${session} is ready to merge. CI passed with no blocking review feedback.`,
	};
}

function readyNotificationFallbackTitle(notification: NotificationDTO): string {
	const titleNumber = notification.title.match(/\bPR\s*#(\d+)\b/i)?.[1];
	const urlNumber = notification.prUrl.match(/\/pull\/(\d+)(?:\/|$)/)?.[1];
	const number = titleNumber ?? urlNumber;
	return number ? `PR #${number} is ready to merge` : "Pull request is ready to merge";
}

function notificationMentions(notification: NotificationCopy, value: string): boolean {
	const needle = value.trim().toLocaleLowerCase();
	if (!needle) return false;
	return `${notification.title}\n${notification.body}`.toLocaleLowerCase().includes(needle);
}

function notificationIcon(type: string) {
	switch (type) {
		case "needs_input":
			return MessageSquareDot;
		case "ready_to_merge":
			return GitPullRequestArrow;
		case "pr_merged":
			return GitMerge;
		case "pr_closed_unmerged":
			return GitPullRequestClosed;
		default:
			return Bell;
	}
}

// Bare colored glyph per type (no tile). Merged uses GitHub's PR-merged purple;
// the accent token is a near-black surface color in this theme and reads as
// invisible, so it must not be used for a glyph.
function notificationIconClass(type: string): string {
	switch (type) {
		case "needs_input":
			return "text-warning";
		case "ready_to_merge":
			return "text-success";
		case "pr_merged":
			return "text-[#a371f7]";
		case "pr_closed_unmerged":
			return "text-error";
		default:
			return "text-muted-foreground";
	}
}
