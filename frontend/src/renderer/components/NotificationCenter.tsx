import { AppLink } from "./AppLink";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useParams } from "@tanstack/react-router";
import {
	ArrowUpRight,
	Bell,
	BellRing,
	Check,
	CheckCheck,
	CircleAlert,
	CircleCheck,
	CircleX,
	Clock,
	GitMerge,
	GitPullRequest,
	GitPullRequestArrow,
	GitPullRequestClosed,
	Hand,
	LoaderCircle,
	MoreHorizontal,
	Mail,
	MailOpen,
	MessageSquareDiff,
	MessageSquareDot,
	SlidersHorizontal,
	X,
} from "lucide-react";
import { Fragment, memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
	useClearAllNotificationsMutation,
	useClearNotificationMutation,
	useMarkAllNotificationsReadMutation,
	useNotificationsQuery,
} from "../hooks/useNotificationsQuery";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useSwipeToDismiss } from "../hooks/useSwipeToDismiss";
import { useRemoteWorkspaces, useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSummary } from "../types/workspace";
import { aoBridge } from "../lib/bridge";
import { formatTimeCompact } from "../lib/format-time";
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
import { captureTopbarAction } from "../lib/topbar-telemetry";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import { cn } from "../lib/utils";
import { TopbarButton } from "./TopbarButton";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "./ui/popover";
import { useCloudNotifications } from "../hooks/useCloudNotifications";
import { useCloudCp } from "../hooks/useCloudCp";
import { clearRemoteNotification, clearRemoteNotifications, connectRemoteNotificationStreams, fetchRemoteNotificationsPage, markRemoteNotificationsRead, remoteNotificationsQueryKey, useRemoteNotificationHosts } from "../hooks/useRemoteNotifications";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpNotification } from "../lib/cloud-cp/types";
import {
	cloudAlertKey,
	cloudSnoozeUntil,
	shouldAlertCloudNotification,
	subscribeLiveCloudNotificationEvents,
	type CloudSnoozeChoice,
} from "../lib/cloud-notifications";
import { approvalDecisionKind, approvalDecisionLabeler, orderedApprovalDecisions, parseApprovalDecisions, type ApprovalDecisionLabels } from "../lib/approval-decisions";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";

type SessionMeta = { projectId: string; projectName: string; sessionName: string };

type NotificationCenterProps = {
	style?: React.CSSProperties;
};

const REMOTE_NOTIFICATION_PREFIX = "remote-notification:";
const CLOUD_NOTIFICATION_PREFIX = "cloud-notification:";
// Bounds for what the runtime remembers about desktop notifications it showed.
const MAX_REMEMBERED_ALERTS = 512;
const MAX_REMEMBERED_TARGETS = 256;
const notificationKey = (hostId: HostId, id: string) => refKey({ host: hostId, id });

function useNotificationTargetNavigation() {
	const navigateToSession = useNavigateToSession();
	const openSession = useCallback(
		(notification: NotificationDTO, hostId: HostId = LOCAL_HOST) => {
			const sessionId = notification.target.sessionId || notification.sessionId;
			if (!sessionId) return;
			recordManualWorkerOpen(sessionId);
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
		sessionMeta: Map<string, SessionMeta>;
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
		const map = new Map<string, SessionMeta>();
		for (const workspace of workspaceQuery.data ?? []) {
			for (const session of workspace.sessions) {
				map.set(sessionUiKey(session.id, workspace.hostId), { projectId: workspace.id, projectName: workspace.name, sessionName: session.title });
			}
		}
		for (const workspace of remoteWorkspaces.data) {
			for (const session of workspace.sessions) {
				map.set(sessionUiKey(session.id, workspace.hostId), { projectId: workspace.id, projectName: workspace.name, sessionName: session.title });
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
	const navigateToSession = useNavigateToSession();
	const unreadQuery = useNotificationsQuery("unread");
	const remoteUnread = useRemoteNotificationHosts("unread");
	// CloudNotificationRuntime keeps the cloud inbox live; this only reads it.
	// On a control plane with team notifications the dock counts what blocks
	// the person there, the same number as the bell's Needs you.
	const cloud = useCloudNotifications();
	const unreadCount = getCachedUnreadCount(unreadQuery.data) + remoteUnread.totalUnreadCount +
		(cloud.mode === "inbox" ? cloud.needsYouCount : 0);
	const params = useParams({ strict: false }) as { hostId?: string; sessionId?: string };
	const routeRef = useRef(params);
	routeRef.current = params;
	const seenRemoteIds = useRef(new Map<HostId, Set<string>>());
	const shownRemoteNotifications = useRef(new Map<string, NotificationDTO>());
	const alertedCloud = useRef(new Set<string>());
	const shownCloudTargets = useRef(new Map<string, { projectId?: string; sessionId: string }>());

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
	useEffect(() => connectRemoteNotificationStreams(queryClient), [queryClient]);

	// Keep the OS launcher badge in sync in the persistent shell runtime;
	// topbar controls can remount as the visible page changes.
	useEffect(() => {
		void aoBridge.notifications.setBadge(unreadCount);
	}, [unreadCount]);

	useEffect(() => {
		for (const host of remoteUnread.hosts) {
			if (!host.data) continue;
			const currentIds = new Set(host.data.notifications.map((item) => item.id));
			const previousIds = seenRemoteIds.current.get(host.hostId);
			if (!previousIds) {
				seenRemoteIds.current.set(host.hostId, currentIds);
				continue; // Reconnecting to a host must not replay its existing inbox.
			}
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
			for (const id of currentIds) {
				previousIds.delete(id);
				previousIds.add(id);
				if (previousIds.size > 512) previousIds.delete(previousIds.values().next().value!);
			}
		}
	}, [remoteUnread.hosts]);

	// Cloud events reach the desktop through the same path local ones use. Only
	// events from a stream opened after the inbox was read are published, so the
	// first load and a reconnect never notify. One notification per session:
	// a newer one replaces it rather than stacking (tag), and each thing that
	// raised a row notifies once however often the row changes.
	useEffect(() => subscribeLiveCloudNotificationEvents((event) => {
		if (!shouldAlertCloudNotification(event)) return;
		const row = event.notification;
		const key = cloudAlertKey(row);
		if (alertedCloud.current.has(key)) return;
		rememberBounded(alertedCloud.current, key, MAX_REMEMBERED_ALERTS);
		const id = `${CLOUD_NOTIFICATION_PREFIX}${row.id}`;
		if (row.sessionId) {
			shownCloudTargets.current.set(id, { projectId: row.projectId, sessionId: row.sessionId });
			if (shownCloudTargets.current.size > MAX_REMEMBERED_TARGETS) {
				shownCloudTargets.current.delete(shownCloudTargets.current.keys().next().value!);
			}
		}
		const { hostId, sessionId } = routeRef.current;
		const watched = !hostId && Boolean(row.sessionId) && sessionId === row.sessionId &&
			document.visibilityState === "visible" && document.hasFocus();
		void aoBridge.notifications.show({
			id,
			title: row.title,
			body: row.body || undefined,
			type: row.type,
			watched,
			tag: `cloud-session:${row.sessionId ?? row.id}`,
		});
	}), []);

	useEffect(() => {
		return aoBridge.notifications.onClick((id) => {
			if (id.startsWith(CLOUD_NOTIFICATION_PREFIX)) {
				const target = shownCloudTargets.current.get(id);
				if (!target) return;
				recordManualWorkerOpen(target.sessionId);
				void captureRendererEvent("ao.renderer.notification_opened", { target: "session" });
				navigateToSession(target.projectId, target.sessionId);
				return;
			}
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
	}, [navigateToSession, openPrimary, queryClient]);

	return null;
}

/** Adds to an insertion-ordered set, dropping the oldest entry past `max`. */
function rememberBounded(set: Set<string>, value: string, max: number) {
	set.add(value);
	if (set.size > max) set.delete(set.values().next().value!);
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
	// CloudNotificationRuntime keeps the cloud lists live; the panel only reads
	// them. Their responses also carry the server's counts for the badge.
	const cloud = useCloudNotifications();
	// A control plane with team notifications keeps read, done and snooze on
	// the server; one from before them is read when the bell opens and cleared
	// on this device, as it always was.
	const inbox = cloud.mode === "inbox";
	const { markRead: markCloudRead, markUnread: markCloudUnread, snooze: snoozeCloudRows, markAllRead: markAllCloudRead } = cloud;
	const cloudTabItems = cloud.all;
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
	// Hold older Cloud rows until the local pagination window reaches them.
	const localHasMore = Boolean(allQuery.hasNextPage);
	const oldestLocal = useMemo(() => notifications.reduce(
		(min, item) => Math.min(min, Date.parse(item.createdAt)),
		Number.POSITIVE_INFINITY,
	), [notifications]);
	const loadedCloud = useCallback((items: CloudCpNotification[]) =>
		localHasMore ? items.filter((item) => Date.parse(item.createdAt) >= oldestLocal) : items, [localHasMore, oldestLocal]);
	const visibleCloudItems = useMemo(() => loadedCloud(cloudTabItems), [cloudTabItems, loadedCloud]);
	const loadedCloudItems = useMemo(() => loadedCloud(cloud.all), [cloud.all, loadedCloud]);
	const entries = useMemo(() => [
		...notifications.map((notification) => ({ hostId: LOCAL_HOST, label: "", notification })),
		...remoteItems.flatMap((host) => host.notifications.map((notification) => ({
			hostId: host.hostId, label: host.label, notification,
		}))),
	].sort((a, b) => b.notification.createdAt.localeCompare(a.notification.createdAt)), [notifications, remoteItems]);
	// The bell counts what blocks the person on a control plane with team
	// notifications, and every unread row everywhere else, as it always has.
	const unreadCount = getCachedUnreadCount(unreadQuery.data) + remoteUnread.totalUnreadCount +
		(inbox ? cloud.needsYouCount : cloud.unreadCount);
	// Local and remote-host questions still waiting on an answer sit in Needs
	// you beside the cloud's blocking rows. The unread cache is always loaded,
	// so the count is right before the history behind the panel arrives.
	const hostNeedsYouCount = useMemo(() => {
		const keys = new Set<string>();
		for (const item of [...getCachedNotifications(unreadQuery.data), ...notifications]) {
			if (hostRowNeedsYou(item)) keys.add(notificationKey(LOCAL_HOST, item.id));
		}
		for (const host of remoteItems) {
			for (const item of host.notifications) if (hostRowNeedsYou(item)) keys.add(notificationKey(host.hostId, item.id));
		}
		return keys.size;
	}, [notifications, remoteItems, unreadQuery.data]);
	const needsYouTabCount = cloud.needsYouCount + hostNeedsYouCount;
	const cloudUnreadIds = useMemo(() => loadedCloudItems.filter((item) => item.status === "unread").map((item) => item.id), [loadedCloudItems]);
	const confirmedClearSnapshot = isNotificationsCacheFromClear(queryClient);
	const { openSession } = useNotificationTargetNavigation();
	const navigateToSession = useNavigateToSession();
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
					await markRemoteNotificationsRead(host.hostId, unreadIds, queryClient);
				} catch (error) {
					for (const id of unreadIds) acknowledgedRemoteIdsRef.current.delete(notificationKey(host.hostId, id));
					setMarkReadError(`${host.label}: ${error instanceof Error ? error.message : t("notify.couldNotMarkAllRead")}`);
				}
			})();
		}
	}, [ackRetryNonce, open, queryClient, t, visibleRemoteUnreadKey]);

	// Cloud rows are acknowledged the same way: concrete loaded ids only, so a
	// row that arrives mid-refresh is highlighted before it is marked read.
	const openRef = useRef(open);
	openRef.current = open;
	// Only on a control plane from before team notifications: with them,
	// opening the bell reads nothing, and rows read when their session opens,
	// when clicked, or with Mark all read.
	const acknowledgedCloudIdsRef = useRef<Set<string>>(new Set());
	const cloudLoading = cloud.isLoading;
	const legacyCloud = cloud.mode === "legacy";
	const cloudUnreadKey = legacyCloud ? cloudUnreadIds.join("|") : "";
	useEffect(() => {
		if (!open) {
			acknowledgedCloudIdsRef.current = new Set();
			return;
		}
		if (cloudLoading) return;
		const unreadIds = cloudUnreadKey === "" ? [] : cloudUnreadKey.split("|");
		const newly = unreadIds.filter((id) => !acknowledgedCloudIdsRef.current.has(id));
		if (newly.length === 0) return;

		for (const id of newly) acknowledgedCloudIdsRef.current.add(id);
		setHighlightedIds((current) => {
			const next = new Set(current);
			for (const id of newly) next.add(cloudRowKey(id));
			return next;
		});
		setMarkReadError(null);
		void markCloudRead(newly).catch((error: unknown) => {
			for (const id of newly) acknowledgedCloudIdsRef.current.delete(id);
			if (openRef.current) {
				setMarkReadError(error instanceof Error ? error.message : t("notify.couldNotMarkAllRead"));
			}
		});
	}, [ackRetryNonce, cloudLoading, cloudUnreadKey, markCloudRead, open, t]);

	const setPanelOpen = useCallback((nextOpen: boolean) => {
		setOpen(nextOpen);
		if (nextOpen) {
			captureTopbarAction("notifications");
			setActionError(null);
		}
		if (!nextOpen) {
			remoteHistoryGeneration.current += 1;
			remoteFirstPageKeys.current.clear();
			setRemoteOlderPages({});
			setRemoteLoadingEarlierHost(null);
			setRemoteEarlierError(null);
			keepLatestNotificationsPage(queryClient, unreadNotificationsQueryKey);
			keepLatestNotificationsPage(queryClient, recentNotificationsQueryKey);
		}
	}, [needsYouTabCount, queryClient]);

	const retryMarkRead = () => {
		setMarkReadError(null);
		setAckRetryNonce((nonce) => nonce + 1);
	};

	const openSessionAndDismiss = useCallback((notification: NotificationDTO, hostId: HostId) => {
		openSession(notification, hostId);
		setPanelOpen(false);
	}, [openSession, setPanelOpen]);

	const restoreThenOpen = useCallback(async (
		sessionId: string,
		hostId: HostId | undefined,
		restoreKey: string,
		openAfterRestore: () => void,
	) => {
		if (restoringSessionId) return;
		setRestoringSessionId(restoreKey);
		setActionError(null);
		try {
			const result = hostId && hostId !== LOCAL_HOST
				? await restoreSession(sessionId, hostId)
				: await restoreSession(sessionId);
			if (result.status === "success") {
				openAfterRestore();
				setPanelOpen(false);
				return;
			}
			setActionError(result.status === "not_resumable" ? t("notify.restoreUnavailable") : result.message);
		} finally {
			setRestoringSessionId(undefined);
		}
	}, [restoreSession, restoringSessionId, setPanelOpen, t]);

	const restoreAndOpen = useCallback((notification: NotificationDTO, hostId: HostId) => {
		const sessionId = notification.target.sessionId || notification.sessionId;
		if (!sessionId) return;
		void restoreThenOpen(sessionId, hostId, sessionUiKey(sessionId, hostId), () => openSession(notification, hostId));
	}, [openSession, restoreThenOpen]);

	// Cloud sessions live in the same workspace tree as local ones, keyed by
	// their cloud project, so they open through the same session route.
	const openCloudSession = useCallback((projectId: string, sessionId: string) => {
		recordManualWorkerOpen(sessionId);
		void captureRendererEvent("ao.renderer.notification_opened", { target: "session" });
		navigateToSession(projectId, sessionId);
	}, [navigateToSession]);

	const openCloudSessionAndDismiss = useCallback((projectId: string, sessionId: string, notification: CloudCpNotification) => {
		// Clicking a row reads it, wherever it leads.
		if (inbox && notification.status === "unread") void markCloudRead([notification.id]).catch(() => undefined);
		openCloudSession(projectId, sessionId);
		setPanelOpen(false);
	}, [inbox, markCloudRead, openCloudSession, setPanelOpen]);

	const restoreCloudAndOpen = useCallback((projectId: string, sessionId: string) => {
		void restoreThenOpen(sessionId, undefined, cloudRowKey(sessionId), () => openCloudSession(projectId, sessionId));
	}, [openCloudSession, restoreThenOpen]);

	const clearCloud = cloud.clearAll;
	const clearCloudOne = cloud.clearOne;
	// Clear local history unless it is confirmed empty. A failed local load still
	// clears server-side: the daemon's clear-all does not need rows loaded.
	const clearLocal = notifications.length > 0 || !allQuery.isSuccess;
	const handleClearAll = useCallback(() => {
		if (clearingAll) return;
		setActionError(null);
		setClearingAll(true);
		remoteHistoryGeneration.current += 1;
		setRemoteOlderPages({});
		setRemoteLoadingEarlierHost(null);
		setRemoteEarlierError(null);
		void Promise.allSettled([
			...(clearLocal ? [clearAll.mutateAsync()] : []),
			...(clearCloud ? [clearCloud()] : []),
			...remoteAll.hosts.map(async (host) => {
				await clearRemoteNotifications(host.hostId, queryClient).catch((error: unknown) => {
					throw new Error(`${host.label}: ${error instanceof Error ? error.message : t("notify.couldNotClearAll")}`);
				});
				setRemoteOlderPages((current) => ({ ...current, [host.hostId]: [] }));
			}),
		]).then((results) => {
			const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
			if (failure) setActionError(failure.reason instanceof Error ? failure.reason.message : t("notify.couldNotClearAll"));
		}).finally(() => setClearingAll(false));
	}, [clearAll, clearCloud, clearLocal, clearingAll, queryClient, remoteAll.hosts, t]);

	/** Resolves to whether the notification was cleared. */
	const handleClear = useCallback((notification: NotificationDTO, hostId: HostId): Promise<boolean> => {
		setActionError(null);
		const key = notificationKey(hostId, notification.id);
		setClearingNotificationIds((current) => new Set(current).add(key));
		return (hostId === LOCAL_HOST ? clearOne.mutateAsync(notification) : (async () => {
			await clearRemoteNotification(hostId, notification.id, queryClient);
			remoteHistoryGeneration.current += 1;
			setRemoteLoadingEarlierHost(null);
			setRemoteOlderPages((current) => ({
				...current,
				[hostId]: (current[hostId] ?? []).map((page) => ({
					...page, notifications: page.notifications.filter((item) => item.id !== notification.id),
				})),
			}));
		})())
			.then(() => true, (error: unknown) => {
				setActionError(error instanceof Error ? error.message : t("notify.couldNotClearOne"));
				return false;
			})
			.finally(() => {
				setClearingNotificationIds((current) => {
					const next = new Set(current);
					next.delete(key);
					return next;
				});
			});
	}, [clearOne, queryClient, t]);

	/** Resolves to whether the notification was cleared (done, on a control plane with team notifications). */
	const handleClearCloud = useCallback((notification: CloudCpNotification): Promise<boolean> => {
		const clearingId = cloudRowKey(notification.id);
		setActionError(null);
		setClearingNotificationIds((current) => new Set(current).add(clearingId));
		return clearCloudOne(notification)
			.then(() => true, (error: unknown) => {
				setActionError(error instanceof Error ? error.message : t("notify.couldNotClearOne"));
				return false;
			})
			.finally(() => {
				setClearingNotificationIds((current) => {
					const next = new Set(current);
					next.delete(clearingId);
					return next;
				});
			});
	}, [clearCloudOne, t]);

	// Triage on a control plane with team notifications. A failure puts the row
	// back (the lists are read again) and says why above the list.
	const runCloudAction = useCallback((key: string, action: () => Promise<void>) => {
		setActionError(null);
		setClearingNotificationIds((current) => new Set(current).add(key));
		void action()
			.catch((error: unknown) => setActionError(error instanceof Error ? error.message : t("notify.actionFailed")))
			.finally(() => {
				setClearingNotificationIds((current) => {
					const next = new Set(current);
					next.delete(key);
					return next;
				});
			});
	}, [t]);
	const snoozeCloud = useCallback((notification: CloudCpNotification, choice: CloudSnoozeChoice) => {
		runCloudAction(cloudRowKey(notification.id), () => snoozeCloudRows([notification.id], cloudSnoozeUntil(choice)));
	}, [runCloudAction, snoozeCloudRows]);
	const toggleCloudRead = useCallback((notification: CloudCpNotification) => {
		runCloudAction(cloudRowKey(notification.id), () => notification.status === "unread"
			? markCloudRead([notification.id])
			: markCloudUnread([notification.id]));
	}, [markCloudRead, markCloudUnread, runCloudAction]);
	const [markingAllRead, setMarkingAllRead] = useState(false);
	const handleMarkAllRead = useCallback(() => {
		setActionError(null);
		setMarkingAllRead(true);
		void markAllCloudRead()
			.catch((error: unknown) => setActionError(error instanceof Error ? error.message : t("notify.couldNotMarkAllRead")))
			.finally(() => setMarkingAllRead(false));
	}, [markAllCloudRead, t]);
	// Answers an approval with the same call and choices as the chat's card.
	// The control plane then resolves the row for everyone it reached.
	const { client: cloudClient } = useCloudCp();
	const refetchCloud = cloud.refetch;
	const decideApproval = useCallback(async (notification: CloudCpNotification, decisionId: string) => {
		const requestId = notification.metadata?.requestId;
		if (typeof requestId !== "string" || !notification.sessionId) return;
		await cloudClient.decideChatApproval(notification.orgId, notification.sessionId, requestId, decisionId);
		void refetchCloud();
	}, [cloudClient, refetchCloud]);
	const approvalLabels = useMemo<ApprovalDecisionLabels>(() => ({
		allowOnce: t("notify.approval.allowOnce"),
		allowAlwaysCommand: t("notify.approval.allowAlwaysCommand"),
		allowAlwaysFiles: t("notify.approval.allowAlwaysFiles"),
		deny: t("notify.approval.deny"),
		alwaysDeny: t("notify.approval.alwaysDeny"),
	}), [t]);
	const openNotificationSettings = useCallback(() => {
		setPanelOpen(false);
		useUiStore.getState().openGlobalSettings("notifications");
	}, [setPanelOpen]);

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
	// Local history only loads once the panel opens, while cloud rows are cached
	// in the background. Hold the list until local arrives so rows don't pop in,
	// and keep a local load failure visible even when cloud rows exist.
	const localEmpty = notifications.length === 0;
	const localLoadFailed = allQuery.isError && localEmpty && !confirmedClearSnapshot;
	const localLoading = allQuery.isLoading && localEmpty;
	const remoteLoadFailed = remoteAll.hosts.some((host) => host.isError);
	const remoteLoading = remoteAll.hosts.some((host) => host.isLoading);
	// One list: what blocks the person first, then everything else, each newest
	// first, with local, remote and Cloud rows interleaved. The groups are only
	// labelled when something blocks the person.
	const cloudNeedsYouIds = useMemo(() => new Set(cloud.needsYou.map((item) => item.id)), [cloud.needsYou]);
	const rowGroups = useMemo(() => {
		const merged = [
			...entries.map((entry) => ({ kind: "host" as const, ...entry })),
			...visibleCloudItems.map((item) => ({ kind: "cloud" as const, item })),
		].sort((a, b) => Date.parse(b.kind === "cloud" ? b.item.createdAt : b.notification.createdAt) - Date.parse(a.kind === "cloud" ? a.item.createdAt : a.notification.createdAt));
		const blocks = (row: (typeof merged)[number]) => row.kind === "cloud" ? cloudNeedsYouIds.has(row.item.id) : hostRowNeedsYou(row.notification);
		const needsYou = merged.filter(blocks);
		const rest = merged.filter((row) => !blocks(row));
		return needsYou.length === 0
			? [{ key: "all", label: undefined, rows: rest }]
			: [{ key: "needs_you", label: t("notify.tab.needsYou"), rows: needsYou }, { key: "updates", label: rest.length > 0 ? t("notify.tab.updates") : undefined, rows: rest }];
	}, [cloudNeedsYouIds, entries, t, visibleCloudItems]);
	const rows = useMemo(() => rowGroups.flatMap((group) => group.rows), [rowGroups]);
	const isEmpty = rows.length === 0;
	const nothingAnywhere = entries.length === 0 && loadedCloudItems.length === 0;
	const emptyMessage = t("notify.emptyAll");

	return (
		<Popover onOpenChange={setPanelOpen} open={open}>
			{/* Keep the drawer anchored to layout bounds while the bell scales on press. */}
			<PopoverAnchor asChild>
				<span className="inline-flex shrink-0">
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
				</span>
			</PopoverAnchor>
			<PopoverContent
				align="end"
				aria-label={t("notify.title")}
				className="flex max-h-[calc(100dvh-5rem)] w-notification-width max-w-[calc(100vw-1rem)] flex-col overflow-hidden rounded-lg p-0 [&_button]:transition-none [&_a]:transition-none"
				sideOffset={6}
			>
				<TooltipProvider skipDelayDuration={0}>
				<div className="flex min-h-11 shrink-0 items-center justify-between gap-2 border-b border-border px-2">
					<h2 className="truncate pl-1.5 text-control font-medium text-foreground">{t("notify.title")}</h2>
					{/* Everything but the title lives in one menu, so the header stays quiet. */}
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("notify.moreActions")} size="icon-sm" type="button" variant="quiet">
								{markingAllRead || clearingAll ? (
									<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
								) : (
									<MoreHorizontal className="size-icon-base" aria-hidden="true" />
								)}
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end">
							{inbox ? (
								<DropdownMenuItem
									disabled={markingAllRead || cloudUnreadIds.length === 0}
									onSelect={() => void handleMarkAllRead()}
								>
									<CheckCheck className="size-icon-sm" aria-hidden="true" />
									{t("notify.markAllRead")}
								</DropdownMenuItem>
							) : null}
							<DropdownMenuItem
								disabled={nothingAnywhere || localLoading || clearingAll || clearAll.isPending}
								onSelect={() => void handleClearAll()}
							>
								<X className="size-icon-sm" aria-hidden="true" />
								{t("notify.clearAll")}
							</DropdownMenuItem>
							{inbox ? (
								<>
									<DropdownMenuSeparator />
									<DropdownMenuItem onSelect={openNotificationSettings}>
										<SlidersHorizontal className="size-icon-sm" aria-hidden="true" />
										{t("notify.settings")}
									</DropdownMenuItem>
								</>
							) : null}
						</DropdownMenuContent>
					</DropdownMenu>
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
						<Button
							className="shrink-0 font-medium"
							variant="link"
							onClick={retryMarkRead}
							type="button"
						>
							{t("notify.retry")}
						</Button>
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
						<Button
							className="shrink-0 font-medium"
							variant="link"
							onClick={retryWorkspace}
							type="button"
						>
							{t("notify.retry")}
						</Button>
					</div>
				) : null}
				{(localLoadFailed || remoteLoadFailed) && !isEmpty ? (
					<div
						aria-live="polite"
						className="flex items-center justify-between gap-2 border-b border-border bg-error/5 px-4 py-2 text-caption text-error"
					>
						<span>{t("notify.loadFailed")}</span>
						<Button
							className="shrink-0 font-medium"
							variant="link"
							onClick={() => {
								if (localLoadFailed) void allQuery.refetch();
								for (const host of remoteAll.hosts) {
									if (host.isError) void queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(host.hostId, "all") });
								}
							}}
							type="button"
						>
							{t("notify.retry")}
						</Button>
					</div>
				) : null}
				{(localLoadFailed || remoteLoadFailed) && isEmpty ? (
					<NotificationEmpty icon={CircleAlert} message={t("notify.loadFailed")} />
				) : localLoading || (remoteLoading && isEmpty) ? (
					<NotificationEmpty icon={LoaderCircle} message={t("notify.loading")} />
				) : isEmpty ? (
					<NotificationEmpty icon={CheckCheck} message={emptyMessage} />
					) : (
						<>
						<div
						aria-busy={allQuery.isFetchingNextPage || remoteLoadingEarlierHost !== null}
						className="board-scrollbar min-h-0 max-h-notification-max-height overflow-x-hidden overflow-y-auto overscroll-contain"
						onScroll={loadEarlierOnScroll}
						role="list"
					>
						{rowGroups.map((group) => (
						<Fragment key={group.key}>
						{group.label ? (
							<div aria-hidden="true" className="px-3.5 pb-1 pt-3 text-caption font-medium text-muted-foreground">
								{group.label}
							</div>
						) : null}
						{group.rows.map((row) => {
							if (row.kind === "cloud") {
								const cloud = row.item;
								const cloudSessionId = cloud.sessionId ?? "";
								const cloudSessionKey = cloudSessionId ? sessionUiKey(cloudSessionId) : undefined;
								const cloudMeta = cloudSessionKey ? sessionMeta.get(cloudSessionKey) : undefined;
								const cloudTerminated = Boolean(cloudMeta) && Boolean(cloudSessionKey && terminatedIds.has(cloudSessionKey));
								const cloudOfferRestore = cloudTerminated && cloud.type === "needs_input";
								const clearingKey = cloudRowKey(cloud.id);
								return (
									<CloudNotificationItem
										highlighted={highlightedIds.has(clearingKey) || cloud.status === "unread"}
										key={clearingKey}
										inbox={inbox}
										notification={cloud}
										onOpenSession={openCloudSessionAndDismiss}
										onRestore={restoreCloudAndOpen}
										onClear={handleClearCloud}
										onSnooze={snoozeCloud}
										onToggleRead={toggleCloudRead}
										onDecide={decideApproval}
										approvalLabels={approvalLabels}
										clearing={clearingNotificationIds.has(clearingKey)}
										clearDisabled={clearingNotificationIds.has(clearingKey) || clearingAll || clearAll.isPending}
										restoring={Boolean(cloudSessionId) && restoringSessionId === cloudRowKey(cloudSessionId)}
										restoreDisabled={restoringSessionId !== undefined}
										projectId={cloudMeta?.projectId}
										projectName={cloudMeta?.projectName}
										sessionName={cloudMeta?.sessionName}
										sessionsReady={sessionsReady}
										terminated={cloudTerminated}
										offerRestore={cloudOfferRestore}
									/>
								);
							}
							const { hostId, label, notification } = row;
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
						</Fragment>
						))}
						{allQuery.isFetchNextPageError ? (
							<div
								aria-live="polite"
								className="flex items-center justify-center gap-2 border-t border-border bg-surface px-3 py-3 text-caption text-error"
							>
								{t("notify.earlierLoadFailed")}
								<Button
									className="font-medium"
									variant="link"
									onClick={() => void allQuery.fetchNextPage()}
									type="button"
								>
									{t("notify.retry")}
								</Button>
							</div>
						) : allQuery.isFetchingNextPage ? (
							<div
								aria-live="polite"
								className="flex items-center justify-center gap-2 border-t border-border bg-surface px-3 py-3 text-caption text-muted-foreground"
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
								<Pressable
									className="block w-full border-t border-border bg-surface px-3 py-3 text-center text-caption font-medium text-muted-foreground transition-none hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
									disabled={remoteLoadingEarlierHost !== null}
									key={host.hostId}
									onClick={() => void loadRemoteEarlier(host.hostId, cursor)}
									type="button"
								>
									{remoteLoadingEarlierHost === host.hostId ? t("notify.loadingEarlier") : t("notify.loadEarlierFromHost", { host: host.label })}
								</Pressable>
							);
						})}
						{remoteEarlierError ? <div aria-live="polite" className="px-4 py-2 text-caption text-error">{remoteEarlierError}</div> : null}
					</div>
						</>
				)}
						</>
					)}
				</NotificationWorkspaceState>
				</TooltipProvider>
			</PopoverContent>
		</Popover>
	);
}

function NotificationEmpty({ icon: Icon, message }: { icon: typeof Bell; message: string }) {
	return (
		<div className="flex flex-col items-center gap-3 px-6 py-10 text-center">
			<div className="grid size-control-xl place-items-center rounded-lg bg-surface text-muted-foreground">
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
	onClear: (notification: NotificationDTO) => Promise<boolean>;
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
	const rowRef = useRef<HTMLDivElement>(null);
	useSwipeToDismiss(rowRef, () => onClear(notification), !clearDisabled);
	return (
		<div ref={rowRef} className="touch-pan-y select-none border-b border-border last:border-b-0" role="listitem">
			<div
				className={cn(
					"group grid grid-cols-[1rem_minmax(0,1fr)_auto] items-start gap-x-2.5 pl-3.5 pr-3 py-3 text-left transition-none hover:bg-interactive-active focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60",
					highlighted && "bg-interactive-hover",
					canOpenSession
						? "cursor-pointer active:bg-interactive-active"
						: "cursor-default",
				)}
				onClick={openRow}
				onKeyDown={(event) => {
					if (!canOpenSession || event.target !== event.currentTarget) return;
					if (event.key !== "Enter" && event.key !== " ") return;
					event.preventDefault();
					openRow();
				}}
				role={canOpenSession ? "button" : undefined}
				tabIndex={canOpenSession ? 0 : undefined}
				// Row actions set title="" so this native tooltip does not show over them.
				title={canOpenSession ? t("notify.openSessionTitle") : undefined}
			>
				<div
					className={cn(
						"flex h-5 shrink-0 items-center justify-center",
						notificationIconClass(notification.type),
					)}
				>
					<Icon className="size-icon-base" strokeWidth={1.75} aria-hidden="true" />
				</div>
				<div className="min-w-0">
					<div className="flex min-h-5 items-start">
						<span
							aria-label={copy.title}
							className={cn(
								"min-w-0 break-words text-control leading-5 text-foreground",
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
						<p className="mt-1 whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
							{copy.body}
						</p>
					) : null}
					{hostLabel || projectName || showSessionMeta || (offerRestore && sessionId) ? (
						<div className="mt-2 flex min-w-0 flex-col items-start gap-1.5">
						{hostLabel || projectName || showSessionMeta ? (
							<p className="flex min-w-0 max-w-full items-center gap-1.5 text-caption leading-4 text-muted-foreground">
								{hostLabel ? <span className="shrink-0 font-medium text-muted-foreground">{hostLabel}</span> : null}
								{hostLabel && (projectName || showSessionMeta) ? <span aria-hidden="true">·</span> : null}
								{projectName ? (
									<span className="truncate font-medium text-muted-foreground">{projectName}</span>
								) : null}
								{projectName && showSessionMeta ? <span aria-hidden="true">·</span> : null}
								{showSessionMeta ? <span className="truncate">{sessionName}</span> : null}
							</p>
						) : null}
						{offerRestore && sessionId ? (
							<Button
								title=""
								className="min-h-6 max-w-full gap-1.5 whitespace-normal text-left text-muted-foreground"
								variant="link"
								disabled={restoreDisabled}
								onClick={(event) => {
									event.stopPropagation();
									onRestore(notification);
								}}
								onKeyDown={(event) => event.stopPropagation()}
								type="button"
							>
								{restoring ? <LoaderCircle className="size-icon-sm shrink-0 animate-spin" aria-hidden="true" /> : null}
								<span className="text-caption leading-snug">{restoring ? t("shell.restoringSession") : t("notify.restoreArchivedPrompt")}</span>
							</Button>
						) : null}
						</div>
					) : null}
				</div>
				<div className="flex min-w-14 shrink-0 flex-col items-end gap-1">
					<time className="flex h-5 items-center whitespace-nowrap text-caption tabular-nums text-muted-foreground" dateTime={notification.createdAt}>
						{formatTimeCompact(notification.createdAt)}
					</time>
					<div className="flex items-center gap-0.5">

					<Button
						aria-label={t("notify.clearOne", { title: hostLabel ? `${hostLabel}: ${copy.title}` : copy.title })}
						title=""
						className={cn(
							"size-notification-icon",
							clearing
								? "opacity-100 disabled:opacity-100"
								: "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100 [@media(hover:none)]:pointer-events-auto [@media(hover:none)]:opacity-100",
						)}
						disabled={clearDisabled}
						onClick={(event) => {
							event.stopPropagation();
							void onClear(notification);
						}}
						onKeyDown={(event) => event.stopPropagation()}
						size="none"
						type="button"
						variant="quiet"
					>
						{clearing ? (
							<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
						) : (
							<X className="size-icon-sm" aria-hidden="true" />
						)}
					</Button>
					</div>
				</div>
			</div>
		</div>
	);
});

const cloudRowKey = (id: string) => `cloud:${id}`;


/** A local or remote-host row that blocks the person: a question not answered yet. */
function hostRowNeedsYou(notification: NotificationDTO): boolean {
	return notification.type === "needs_input" && !notification.resolvedAt;
}

/** Why the person got a row, as the control plane names it. "named" (sent to them by name) needs no label. */
const CLOUD_REASON_KEYS = {
	owner: "notify.reason.owner",
	prompted: "notify.reason.prompted",
	following: "notify.reason.following",
	mentioned: "notify.reason.mentioned",
	requester: "notify.reason.requester",
	handoff: "notify.reason.handoff",
	reviewer: "notify.reason.reviewer",
	asided: "notify.reason.asided",
} as const;

function cloudReasonKey(reason: string | undefined) {
	return reason && reason in CLOUD_REASON_KEYS ? CLOUD_REASON_KEYS[reason as keyof typeof CLOUD_REASON_KEYS] : undefined;
}

/** A blocking row that stopped blocking: someone answered it, or it went away. */
function cloudResolutionText(notification: CloudCpNotification, t: TFunction): string | undefined {
	if (notification.tier !== 0 || !notification.resolvedAt) return undefined;
	if (notification.resolution === "handled") {
		return notification.resolvedBy?.name ? t("notify.handledBy", { name: notification.resolvedBy.name }) : t("notify.handled");
	}
	return t("notify.noLongerNeeded");
}

/**
 * A cloud row. On a control plane from before team notifications it mirrors
 * NotificationItem exactly (open, restore, clear, meta line, motion). With
 * team notifications it also says why the person got it and who caused it,
 * shows a resolved blocking row as a quiet record, offers Done, Snooze and
 * Mark unread in place of Clear, and answers an approval in place. Either
 * way it carries a small "Cloud" tag, and opens only once its session is in
 * the workspace tree; otherwise the route would have nothing to render.
 */
const CloudNotificationItem = memo(function CloudNotificationItem({
	highlighted,
	inbox,
	notification,
	offerRestore,
	onOpenSession,
	onRestore,
	onClear,
	onSnooze,
	onToggleRead,
	onDecide,
	approvalLabels,
	projectId,
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
	/** The control plane has team notifications: triage lives on the server. */
	inbox: boolean;
	notification: CloudCpNotification;
	offerRestore: boolean;
	onOpenSession: (projectId: string, sessionId: string, notification: CloudCpNotification) => void;
	onRestore: (projectId: string, sessionId: string) => void;
	/** Clear on this device, or mark done with team notifications. */
	onClear: (notification: CloudCpNotification) => Promise<boolean>;
	onSnooze: (notification: CloudCpNotification, choice: CloudSnoozeChoice) => void;
	onToggleRead: (notification: CloudCpNotification) => void;
	onDecide: (notification: CloudCpNotification, decisionId: string) => Promise<void>;
	approvalLabels: ApprovalDecisionLabels;
	projectId?: string;
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
	const resolution = inbox ? cloudResolutionText(notification, t) : undefined;
	const Icon = resolution ? CheckCheck : notificationIcon(notification.type);
	const sessionId = notification.sessionId ?? "";
	const canOpenSession = Boolean(sessionId && projectId) && sessionsReady && (!terminated || !offerRestore);
	const showSessionMeta = Boolean(sessionName) && !notificationMentions(notification, sessionName ?? "");
	const reasonKey = inbox ? cloudReasonKey(notification.reason) : undefined;
	const actor = inbox ? notification.actor?.name : undefined;
	const approval = inbox && !resolution && notification.type === "approval_needed" && notification.tier === 0
		? parseApprovalDecisions(notification.metadata?.decisions)
		: [];
	const openRow = () => {
		if (canOpenSession && projectId) onOpenSession(projectId, sessionId, notification);
	};
	const rowRef = useRef<HTMLDivElement>(null);
	useSwipeToDismiss(rowRef, () => onClear(notification), !clearDisabled);
	// The footer says only where a row comes from. Why you got it and who acted
	// are in the title or the row's description, so the row stays one glance:
	// a title, one line of detail, the project. A session name that repeats
	// its project's (an orchestrator) is left out.
	const reasonText = reasonKey ? t(reasonKey) : undefined;
	const metaParts = [
		projectName ? { text: projectName, fixed: false } : undefined,
		!inbox && showSessionMeta && sessionName && sessionName !== projectName ? { text: sessionName, fixed: false } : undefined,
	].filter((part): part is { text: string; fixed: boolean } => Boolean(part));
	return (
		<div ref={rowRef} className="touch-pan-y select-none border-b border-border last:border-b-0" data-notification-source="cloud" role="listitem">
			<div
				className={cn(
					"group grid grid-cols-[1rem_minmax(0,1fr)_auto] items-start gap-x-2.5 pl-3.5 pr-3 py-3 text-left transition-none hover:bg-interactive-active focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60",
					highlighted && "bg-interactive-hover",
					canOpenSession
						? "cursor-pointer active:bg-interactive-active"
						: "cursor-default",
				)}
				onClick={openRow}
				onKeyDown={(event) => {
					if (!canOpenSession || event.target !== event.currentTarget) return;
					if (event.key !== "Enter" && event.key !== " ") return;
					event.preventDefault();
					openRow();
				}}
				role={canOpenSession ? "button" : undefined}
				tabIndex={canOpenSession ? 0 : undefined}
				// Row actions set title="" so this native tooltip does not show over them.
				title={canOpenSession ? t("notify.openSessionTitle") : undefined}
				aria-description={[reasonText, actor].filter(Boolean).join(" · ") || undefined}
			>
				<div
					className={cn(
						"flex h-5 shrink-0 items-center justify-center",
						resolution ? "text-passive" : notificationIconClass(notification.type),
					)}
				>
					<Icon className="size-icon-base" strokeWidth={1.75} aria-hidden="true" />
				</div>
				<div className="min-w-0">
					<div className="flex min-h-5 items-start gap-1.5">
						<span
							aria-label={notification.title}
							className={cn(
								"min-w-0 text-control leading-5",
								inbox ? "truncate" : "break-words",
								resolution ? "text-muted-foreground" : "text-foreground",
								highlighted && !resolution && "font-medium",
							)}
							title={inbox ? notification.title : undefined}
						>
							{notification.title}
						</span>
						{inbox ? null : (
							<span className="mt-0.5 shrink-0 rounded-sm border border-border bg-surface px-1 py-0.5 text-caption leading-none text-muted-foreground">
								{t("notify.cloud")}
							</span>
						)}
					</div>
					{resolution ? (
						<p className="mt-0.5 truncate text-xs leading-relaxed text-passive">{resolution}</p>
					) : notification.body ? (
						<p
							className={cn(
								"mt-0.5 text-xs leading-relaxed text-muted-foreground",
								inbox ? "truncate" : "whitespace-pre-wrap break-words",
								approval.length > 0 && typeof notification.metadata?.summary === "string" && notification.metadata.summary !== "" && "font-mono",
							)}
							title={inbox ? notification.body : undefined}
						>
							{notification.body}
						</p>
					) : null}
					{metaParts.length > 0 || (offerRestore && sessionId && projectId) ? (
						<div className={cn("flex min-w-0 flex-col items-start gap-1.5", inbox ? "mt-1" : "mt-2")}>
						{metaParts.length > 0 ? (
							<p className="flex min-w-0 max-w-full items-center gap-1.5 text-caption leading-4 text-muted-foreground">
								{metaParts.map((part, index) => (
									<span className="contents" key={`${index}:${part.text}`}>
										{index > 0 ? <span aria-hidden="true">·</span> : null}
										<span className={cn(part.fixed ? "shrink-0 whitespace-nowrap" : "min-w-0 truncate", index === 0 && (reasonKey || projectName) && "font-medium")}>{part.text}</span>
									</span>
								))}
							</p>
						) : null}
						{offerRestore && sessionId && projectId ? (
							<Button
								title=""
								className="min-h-6 max-w-full gap-1.5 whitespace-normal text-left text-muted-foreground"
								variant="link"
								disabled={restoreDisabled}
								onClick={(event) => {
									event.stopPropagation();
									onRestore(projectId, sessionId);
								}}
								onKeyDown={(event) => event.stopPropagation()}
								type="button"
							>
								{restoring ? <LoaderCircle className="size-icon-sm shrink-0 animate-spin" aria-hidden="true" /> : null}
								<span className="text-caption leading-snug">{restoring ? t("shell.restoringSession") : t("notify.restoreArchivedPrompt")}</span>
							</Button>
						) : null}
						</div>
					) : null}
					{/* Actions last, as an approval in Slack or Teams: the primary
					    one first, flush with the text above. */}
					{approval.length > 0 && sessionId ? (
						<CloudApprovalActions
							decisions={approval}
							labels={approvalLabels}
							notification={notification}
							onDecide={onDecide}
						/>
					) : null}
				</div>
				<div className="flex min-w-14 shrink-0 flex-col items-end gap-1">
					<time className="flex h-5 items-center whitespace-nowrap text-caption tabular-nums text-muted-foreground" dateTime={notification.createdAt}>
						{formatTimeCompact(notification.createdAt)}
					</time>
					{inbox ? (
						<CloudTriageActions
							busy={clearing}
							disabled={clearDisabled}
							notification={notification}
							onDone={() => void onClear(notification)}
							onSnooze={(choice) => onSnooze(notification, choice)}
							onToggleRead={() => onToggleRead(notification)}
						/>
					) : (
						<div className="flex items-center gap-0.5">
							<Button
								aria-label={t("notify.clearOne", { title: notification.title })}
								title=""
								className={cn("size-notification-icon", clearing ? "opacity-100 disabled:opacity-100" : ROW_ACTION_REVEAL)}
								disabled={clearDisabled}
								onClick={(event) => {
									event.stopPropagation();
									void onClear(notification);
								}}
								onKeyDown={(event) => event.stopPropagation()}
								size="none"
								type="button"
								variant="quiet"
							>
								{clearing ? (
									<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
								) : (
									<X className="size-icon-sm" aria-hidden="true" />
								)}
							</Button>
						</div>
					)}
				</div>
			</div>
		</div>
	);
});

// Row actions stay out of the way until the row is hovered or focused; on a
// touch screen there is no hover, so they always show.
const ROW_ACTION_REVEAL = "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100 data-[state=open]:pointer-events-auto data-[state=open]:opacity-100 [@media(hover:none)]:pointer-events-auto [@media(hover:none)]:opacity-100";

/** Done, Snooze and Mark unread (or read) for a row on a control plane with team notifications. */
function CloudTriageActions({ busy, disabled, notification, onDone, onSnooze, onToggleRead }: {
	busy: boolean;
	disabled: boolean;
	notification: CloudCpNotification;
	onDone: () => void;
	onSnooze: (choice: CloudSnoozeChoice) => void;
	onToggleRead: () => void;
}) {
	const { t } = useTranslation();
	const unread = notification.status === "unread";
	const stop = (event: React.SyntheticEvent) => event.stopPropagation();
	const readLabel = unread ? t("notify.markRead") : t("notify.markUnread");
	return (
		<div className="flex items-center gap-0.5" onClick={stop} onKeyDown={stop}>
			{busy ? <LoaderCircle className="size-icon-sm animate-spin text-muted-foreground" aria-hidden="true" /> : null}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button aria-label={readLabel} className={cn("size-notification-icon", ROW_ACTION_REVEAL)} disabled={disabled} onClick={onToggleRead} size="none" title="" type="button" variant="quiet">
						{unread ? <MailOpen className="size-icon-sm" aria-hidden="true" /> : <Mail className="size-icon-sm" aria-hidden="true" />}
					</Button>
				</TooltipTrigger>
				<TooltipContent side="bottom">{readLabel}</TooltipContent>
			</Tooltip>
			<DropdownMenu>
				<Tooltip>
					<TooltipTrigger asChild>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("notify.snooze")} className={cn("size-notification-icon", ROW_ACTION_REVEAL)} disabled={disabled} size="none" title="" type="button" variant="quiet">
								<Clock className="size-icon-sm" aria-hidden="true" />
							</Button>
						</DropdownMenuTrigger>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t("notify.snooze")}</TooltipContent>
				</Tooltip>
				<DropdownMenuContent align="end" onClick={stop}>
					<DropdownMenuItem onSelect={() => onSnooze("hour")}>{t("notify.snoozeHour")}</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => onSnooze("tomorrow")}>{t("notify.snoozeTomorrow")}</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<Tooltip>
				<TooltipTrigger asChild>
					<Button aria-label={t("notify.done")} className={cn("size-notification-icon", ROW_ACTION_REVEAL)} disabled={disabled} onClick={onDone} size="none" title="" type="button" variant="quiet">
						<Check className="size-icon-sm" aria-hidden="true" />
					</Button>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("notify.done")}</TooltipContent>
			</Tooltip>
		</div>
	);
}

/**
 * Answers an approval from the bell with the two choices the chat card leads
 * with: allow once (first, as the primary action), or deny. Anything more (always allow, always deny) is a
 * decision worth making in the session itself, which the row opens. Only
 * whoever is driving the session or owns it may answer; anyone else is told so
 * and the row stays.
 */
function CloudApprovalActions({ decisions, labels, notification, onDecide }: {
	decisions: ReturnType<typeof parseApprovalDecisions>;
	labels: ApprovalDecisionLabels;
	notification: CloudCpNotification;
	onDecide: (notification: CloudCpNotification, decisionId: string) => Promise<void>;
}) {
	const { t } = useTranslation();
	const [pending, setPending] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	const ordered = orderedApprovalDecisions(decisions);
	const label = approvalDecisionLabeler(ordered, undefined, labels);
	const pick = (...kinds: string[]) => kinds.map((kind) => ordered.find((decision) => approvalDecisionKind(decision) === kind)).find(Boolean);
	const quick = [pick("allow_once", "allow_always"), pick("reject_once", "reject_always")].filter((decision): decision is (typeof ordered)[number] => Boolean(decision));
	const shown = quick.length > 0 ? quick : ordered.slice(0, 2);
	const decide = (decisionId: string) => {
		if (pending) return;
		setPending(decisionId);
		setError(null);
		void onDecide(notification, decisionId)
			.catch((failure: unknown) => {
				setError(failure instanceof CloudCpError && failure.status === 403
					? t("notify.approval.forbidden")
					: failure instanceof Error ? failure.message : t("notify.actionFailed"));
			})
			.finally(() => setPending(null));
	};
	const stop = (event: React.SyntheticEvent) => event.stopPropagation();
	return (
		<div className="mt-2.5 flex flex-col items-start gap-1.5" onClick={stop} onKeyDown={stop}>
			<div className="flex items-center gap-2">
				{shown.map((decision) => {
					const kind = approvalDecisionKind(decision);
					const allows = kind === "allow_once" || kind === "allow_always";
					return (
						<Button
							disabled={pending !== null}
							key={decision.id}
							onClick={() => decide(decision.id)}
							size="sm"
							type="button"
							variant={allows ? "accent" : "outline"}
						>
							{pending === decision.id ? <LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" /> : null}
							{label(decision)}
						</Button>
					);
				})}
			</div>
			{error ? <p aria-live="polite" className="text-caption text-error">{error}</p> : null}
		</div>
	);
}

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
		case "approval_needed":
			return Hand;
		case "needs_input":
			return MessageSquareDot;
		case "merge_requested":
			return GitPullRequestArrow;
		case "review_feedback":
		case "review_changes_requested":
			return MessageSquareDiff;
		case "review_completed":
			return CheckCheck;
		case "ready_to_merge":
			return GitPullRequestArrow;
		case "agent_failed":
			return CircleAlert;
		case "ci_failed":
			return CircleX;
		case "agent_completed":
			return CheckCheck;
		case "turn_finished":
			return CircleCheck;
		case "pr_opened":
			return GitPullRequest;
		case "pr_merged":
		case "merge_request_merged":
			return GitMerge;
		case "pr_closed_unmerged":
		case "merge_request_rejected":
			return GitPullRequestClosed;
		default:
			return Bell;
	}
}

// Match the semantic status colors used by the board and inspector.
function notificationIconClass(type: string): string {
	switch (type) {
		case "approval_needed":
		case "merge_requested":
			return "text-status-needs-you";
		case "needs_input":
		case "review_feedback":
		case "review_changes_requested":
			return "text-warning";
		case "agent_failed":
		case "ci_failed":
			return "text-error";
		case "agent_completed":
		case "pr_opened":
		case "ready_to_merge":
		case "review_completed":
			return "text-success";
		case "pr_merged":
		case "merge_request_merged":
			return "text-[var(--color-status-merged)]";
		case "pr_closed_unmerged":
		case "merge_request_rejected":
			return "text-error";
		default:
			return "text-muted-foreground";
	}
}
