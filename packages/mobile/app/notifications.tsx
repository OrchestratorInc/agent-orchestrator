import { Feather } from "../lib/icons";
import { useFocusEffect, useRouter, type Href } from "expo-router";
import { useOpenPage } from "../lib/pageNavigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	ActivityIndicator,
	Alert,
	Pressable,
	RefreshControl,
	SectionList,
	StyleSheet,
	Text,
	View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import {
	clearNotification,
	getNotifications,
	markAllNotificationsRead,
	markNotificationRead,
	type NotificationRecord,
} from "../lib/api";
import { haptics } from "../lib/haptics";
import { NotificationTypeIcon } from "../lib/notification-type-icon";
import {
	notificationsAcrossHosts,
	notificationSections,
	notificationAction,
	notificationTarget,
	notificationVisual,
	relativeTime,
	type HostedNotification,
} from "../lib/notificationView";
import { useApp } from "../lib/store";
import { MINUTE_MS, useNow } from "../lib/useNow";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { Button, Dot, EmptyState, HeaderIconButton, ScreenHeader } from "../lib/ui";
import { UnpairedState } from "../lib/UnpairedState";
import { space, type } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";
import { userFacingError } from "../lib/connectionError";
import { NotificationSwipeRow } from "../lib/NotificationSwipeRow";
import { MachineIdentity } from "../lib/MachineIdentity";
import { machineAppearance } from "../lib/machineAppearance";
import { useCloudNotifications } from "../lib/cloud/notifications";
import { cloudRequestErrorCopy } from "../lib/cloud/errorCopy";
import { resourceKey, type SourceRef } from "../lib/environment/scopedBoard";
import type { CloudNotification } from "@aoagents/cloud-client";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

const PAGE_SIZE = 50;
type FeedItem =
	| (HostedNotification<NotificationRecord> & { environment: "local" })
	| (CloudNotification & { environment: "cloud"; scopeKey: string; ownerKey: string; sourceRef: SourceRef });

const feedKey = (item: FeedItem, id = item.id) => JSON.stringify([
	item.environment, item.environment === "cloud" ? item.ownerKey : item.hostId, id,
]);
type HostFeed = {
	hostId: string;
	hostName: string;
	items: NotificationRecord[];
	nextCursor?: string;
	unreadCount: number;
	error?: string;
};

// The durable record of what the daemon has notified about. Push only reaches a
// phone that was reachable at the time; this list is what the user can come back
// to afterwards.
export default function NotificationsScreen() {
	return <NotificationsContent />;
}

function NotificationsContent() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const openPage = useOpenPage();
	const insets = useSafeAreaInsets();
	const { hostStates, configResolved, configForHost, restoreOn, scopedBoard, refreshSource } = useApp();
	const cloud = useCloudNotifications();
	const cloudRef = useRef(cloud); cloudRef.current = cloud;
	const [restoringId, setRestoringId] = useState<string>();
	useEffect(() => { setRestoringId((key) => key?.startsWith('["cloud"') ? undefined : key); }, [cloud.ownerKey]);
	// A brief line rather than an Alert: the row is still there to act on, and
	// a modal would make a dead tap feel like an error.
	const [notice, setNotice] = useState<string>();
	const now = useNow(MINUTE_MS);
	const [feeds, setFeeds] = useState<Record<string, HostFeed>>({});
	const feedsRef = useRef(feeds);
	feedsRef.current = feeds;
	const hostsRef = useRef(hostStates);
	hostsRef.current = hostStates;
	const configForHostRef = useRef(configForHost);
	configForHostRef.current = configForHost;
	const loadGeneration = useRef(0);
	const loadingMoreRef = useRef(false);
	const loadedOnceRef = useRef(false);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [loadingMore, setLoadingMore] = useState(false);
	const localItems = useMemo(() => notificationsAcrossHosts(Object.values(feeds).map((feed) => {
		const host = hostStates.find((candidate) => candidate.hostId === feed.hostId);
		return {
			hostId: feed.hostId,
			hostName: feed.hostName,
			hostAppearance: machineAppearance({ id: feed.hostId, name: feed.hostName, appearance: host?.appearance }),
			items: feed.items,
		};
	})), [feeds, hostStates]);
	const items = useMemo<FeedItem[]>(() => [
		...localItems.map((item) => ({ ...item, environment: "local" as const })),
		...(cloud.source && cloud.scopeKey && cloud.ownerKey ? cloud.items.map((item) => ({ ...item, environment: "cloud" as const, sourceRef: cloud.source!, scopeKey: cloud.scopeKey!, ownerKey: cloud.ownerKey! })) : []),
	].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt) || feedKey(left).localeCompare(feedKey(right))), [localItems, cloud.items, cloud.source, cloud.scopeKey, cloud.ownerKey]);
	const sections = useMemo(() => notificationSections(items), [items]);
	const unreadCount = useMemo(() => Object.values(feeds).reduce((count, feed) => count + feed.unreadCount, 0) + cloud.unreadCount, [feeds, cloud.unreadCount]);
	const unavailableFeeds = useMemo(() => Object.values(feeds).filter((feed) => feed.error), [feeds]);
	const hasMore = useMemo(() => Object.values(feeds).some((feed) => Boolean(feed.nextCursor)) || cloud.hasMore, [feeds, cloud.hasMore]);
	const knownHostIds = useMemo(() => new Set(hostStates.map((host) => host.hostId)), [hostStates]);
	const hostSignature = hostStates.map((host) => {
		const source = host.config;
		return `${host.hostId}:${source ? `${source.host}:${source.httpPort}:${source.secure ? "secure" : "plain"}` : "offline"}`;
	}).join("|");

	const updateFeeds = useCallback((update: (current: Record<string, HostFeed>) => Record<string, HostFeed>) => {
		setFeeds((current) => {
			const next = update(current);
			feedsRef.current = next;
			return next;
		});
	}, []);

	const load = useCallback(
		async (mode: "initial" | "refresh" | "more" | "reconcile") => {
			if (mode === "more" && loadingMoreRef.current) return;
			const hosts = hostsRef.current;
			const currentFeeds = feedsRef.current;
			const generation = mode === "more" ? loadGeneration.current : ++loadGeneration.current;
			const targets = hosts.flatMap((host) => {
				const source = configForHost(host.hostId);
				const cursor = currentFeeds[host.hostId]?.nextCursor;
				if (!source || (mode === "more" && !cursor)) return [];
				return [{ host, source, cursor: mode === "more" ? cursor : undefined }];
			});
			if (mode === "initial" && !loadedOnceRef.current) setLoading(true);
			if (mode === "refresh") setRefreshing(true);
			if (mode === "more") {
				loadingMoreRef.current = true;
				setLoadingMore(true);
			}
			const results = await Promise.all(targets.map(async ({ host, source, cursor }) => {
				try {
					const page = await getNotifications(source, { status: "all", limit: PAGE_SIZE, cursor });
					return { host, source, page } as const;
				} catch (cause) {
					return { host, source, error: userFacingError(cause, `Couldn't load notifications from ${host.name}.`) } as const;
				}
			}));
			if (generation !== loadGeneration.current) return;
			updateFeeds((previous) => {
				const paired = new Set(hosts.map((host) => host.hostId));
				const next = Object.fromEntries(Object.entries(previous).filter(([hostId]) => paired.has(hostId)));
				for (const host of hosts) {
					if (configForHost(host.hostId)) continue;
					const retained = next[host.hostId];
					next[host.hostId] = {
						hostId: host.hostId,
						hostName: host.name,
						items: retained?.items ?? [],
						nextCursor: retained?.nextCursor,
						unreadCount: retained?.unreadCount ?? host.notificationsUnread,
						error: `${host.name} is offline.`,
					};
				}
				for (const result of results) {
					if (configForHost(result.host.hostId) !== result.source) continue;
					const previousFeed = next[result.host.hostId];
					if (mode === "reconcile" && previousFeed !== currentFeeds[result.host.hostId]) continue;
					if ("error" in result) {
						next[result.host.hostId] = {
							hostId: result.host.hostId,
							hostName: result.host.name,
							items: previousFeed?.items ?? [],
							nextCursor: previousFeed?.nextCursor,
							unreadCount: previousFeed?.unreadCount ?? result.host.notificationsUnread,
							error: result.error,
						};
						continue;
					}
					const priorItems = mode === "more" || mode === "reconcile" ? previousFeed?.items ?? [] : [];
					const seen = new Set(priorItems.map((item) => item.id));
					const incoming = new Set(result.page.notifications.map((item) => item.id));
					next[result.host.hostId] = {
						hostId: result.host.hostId,
						hostName: result.host.name,
						items: mode === "reconcile" ? [...result.page.notifications, ...priorItems.filter((item) => !incoming.has(item.id))]
							: [...priorItems, ...result.page.notifications.filter((item) => !seen.has(item.id))],
						nextCursor: result.page.nextCursor,
						unreadCount: result.page.unreadCount,
					};
				}
				return next;
			});
			loadedOnceRef.current = true;
			setLoading(false);
			setRefreshing(false);
			loadingMoreRef.current = false;
			setLoadingMore(false);
		},
		[configForHost, updateFeeds],
	);
	const loadRef = useRef(load);
	loadRef.current = load;

	useEffect(() => {
		void load("initial");
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [hostSignature]);
	const refresh = useCallback(async () => {
		await Promise.all([load("refresh"), cloud.refresh().catch(() => {})]);
	}, [load, cloud.refresh]);
	useFocusEffect(useCallback(() => { void cloud.refresh().catch(() => {}); }, [cloud.refresh]));
	function ownsCloud(notification: Extract<FeedItem, { environment: "cloud" }>) {
		return cloudRef.current.ownerKey === notification.ownerKey && cloudRef.current.scopeKey === notification.scopeKey && cloudRef.current.source?.id === notification.sourceRef.id;
	}
	function itemState(notification: FeedItem) {
		if (notification.environment === "local") return sessionState(notification.hostId, notification.sessionId);
		const entry = notification.sessionId ? [...scopedBoard.sessions, ...scopedBoard.orchestrators]
			.find((entry) => resourceKey(entry.source, entry.value.id) === resourceKey(notification.sourceRef, notification.sessionId!))?.value : undefined;
		return { terminated: Boolean(entry && ("projectName" in entry ? entry.isTerminal : entry.isTerminated)), sessionsReady: Boolean(entry) };
	}
	function actionFor(notification: FeedItem) {
		return notificationAction(notification, itemState(notification), notification.environment);
	}

	function open(notification: FeedItem) {
		if (notification.environment === "cloud") {
			if (!ownsCloud(notification)) return;
			haptics.tap();
			if (notification.status === "unread") void cloud.markRead([notification.id]).catch((cause) => {
				if (ownsCloud(notification)) setNotice(cloudRequestErrorCopy(cause));
			});
			const action = actionFor(notification);
			if (action.kind === "open") router.navigate({ pathname: "/session/[id]", params: { id: action.sessionId, source: "cloud", sourceId: notification.sourceRef.id } });
			else if (action.kind === "restore") setNotice("This session is archived. Tap restore to bring it back.");
			else {
				setNotice("Refreshing Cloud session. Try opening it again shortly.");
				void refreshSource(notification.sourceRef).catch((cause) => { if (ownsCloud(notification)) setNotice(cloudRequestErrorCopy(cause)); });
			}
			return;
		}
		const source = configForHost(notification.hostId);
		if (!source) {
			haptics.warning();
			setNotice(`${notification.hostName} is offline.`);
			return;
		}
		haptics.tap();
		updateFeeds((previous) => {
			const feed = previous[notification.hostId];
			if (!feed) return previous;
			return { ...previous, [notification.hostId]: {
				...feed,
				items: feed.items.map((item) => item.id === notification.id ? { ...item, status: "read" } : item),
				unreadCount: notification.status === "unread" ? Math.max(0, feed.unreadCount - 1) : feed.unreadCount,
			} };
		});
		if (notification.status === "unread") {
			markNotificationRead(source, notification.id).catch(() => void load("refresh"));
		}
		// What a tap does depends on the session behind it, exactly as the renderer
		// decides: a terminated agent waiting on input is restored, not opened.
		const action = notificationAction(notification, sessionState(notification.hostId, notification.sessionId));
		if (action.kind === "open") router.navigate({ pathname: "/session/[id]", params: { id: action.sessionId, hostId: notification.hostId, source: "local", sourceId: notification.hostId } });
		else if (action.kind === "review") openPage(notificationTarget(notification, knownHostIds) as Href);
		else if (action.kind === "prs") router.navigate({ pathname: "/prs", params: { hostId: notification.hostId } });
		else if (action.kind === "restore") {
			haptics.warning();
			setNotice("This session is terminated. Tap restore to bring it back.");
		} else if (action.kind === "none") {
			haptics.warning();
			setNotice("That session is not available yet.");
		}
	}

	function clear(notification: FeedItem) {
		if (notification.environment === "cloud") {
			if (!ownsCloud(notification)) return false;
			haptics.warning();
			// Cloud owns the optimistic hide, server read, persistence, and rollback.
			const record = cloud.items.find((item) => item.id === notification.id);
			if (!record) return false;
			void cloud.clearOne(record).catch((cause) => {
				if (ownsCloud(notification)) { haptics.error(); setNotice(cloudRequestErrorCopy(cause)); }
			});
			return true;
		}
		const source = configForHost(notification.hostId);
		if (!source) {
			haptics.warning();
			setNotice(`${notification.hostName} is offline.`);
			return false;
		}
		haptics.warning();
		updateFeeds((previous) => {
			const feed = previous[notification.hostId];
			if (!feed || !feed.items.some((item) => item.id === notification.id)) return previous;
			return { ...previous, [notification.hostId]: {
				...feed,
				items: feed.items.filter((item) => item.id !== notification.id),
				unreadCount: notification.status === "unread" ? Math.max(0, feed.unreadCount - 1) : feed.unreadCount,
			} };
		});
		void clearNotification(source, notification.id).catch((cause) => {
			haptics.error();
			setNotice(userFacingError(cause, "Couldn't clear that notification."));
			updateFeeds((previous) => {
				const feed = previous[notification.hostId];
				if (!feed || feed.items.some((item) => item.id === notification.id)) return previous;
				return { ...previous, [notification.hostId]: {
					...feed,
					items: [notification, ...feed.items],
					unreadCount: notification.status === "unread" ? feed.unreadCount + 1 : feed.unreadCount,
				} };
			});
		});
		return true;
	}

	function sessionState(hostId: string, sessionId?: string) {
		const host = hostStates.find((item) => item.hostId === hostId);
		const session = sessionId ? host?.sessions.find((item) => item.id === sessionId) : undefined;
		return {
			terminated: Boolean(session?.isTerminated || session?.status === "terminated"),
			// Without the board we cannot tell a terminated session from a live one,
			// and guessing lands on a screen that cannot resolve it.
			sessionsReady: Boolean(host && !host.loading && host.sessions.length > 0),
		};
	}

	function restoreSession(notification: FeedItem) {
		const sessionId = notification.sessionId;
		if (!sessionId || actionFor(notification).kind !== "restore" || restoringId) return;
		if (notification.environment === "cloud" && !ownsCloud(notification)) return;
		const key = feedKey(notification, sessionId);
		const source: SourceRef = notification.environment === "cloud" ? notification.sourceRef : { kind: "local", id: notification.hostId };
		const current = () => notification.environment === "cloud" ? ownsCloud(notification) : Boolean(configForHost(notification.hostId));
		haptics.tap();
		setRestoringId(key);
		void restoreOn(source, sessionId)
			.then(() => {
				if (!current()) return;
				haptics.success();
				router.navigate({ pathname: "/session/[id]", params: { id: sessionId, source: source.kind, sourceId: source.id, ...(source.kind === "local" ? { hostId: source.id } : {}) } });
			})
			.catch((cause) => { if (current()) Alert.alert("Couldn't restore the session", source.kind === "cloud" ? cloudRequestErrorCopy(cause) : userFacingError(cause)); })
			.finally(() => setRestoringId((current) => current === key ? undefined : current));
	}

	async function markAll() {
		const targets = Object.values(feedsRef.current).flatMap((feed) => {
			const source = feed.unreadCount > 0 ? configForHost(feed.hostId) : null;
			return source ? [{ feed, source }] : [];
		});
		const cloudRead = cloud.source && cloud.unreadCount > 0;
		const ownerKey = cloud.ownerKey;
		if (targets.length === 0 && !cloudRead) {
			haptics.warning();
			setNotice("Unread notifications are on machines that are currently offline.");
			return;
		}
		haptics.success();
		const ownsLocal = ({ feed, source }: typeof targets[number]) => hostsRef.current.some((host) => host.hostId === feed.hostId)
			&& configForHostRef.current(feed.hostId) === source;
		const localRequests = targets.map(async (target) => {
			try {
				await markAllNotificationsRead(target.source);
				const capturedIds = new Set(target.feed.items.map((item) => item.id));
				updateFeeds((previous) => {
					const feed = previous[target.feed.hostId];
					if (!ownsLocal(target) || !feed) return previous;
					const acknowledged = feed.items.filter((item) => capturedIds.has(item.id) && item.status === "unread").length;
					return { ...previous, [feed.hostId]: { ...feed,
						items: feed.items.map((item) => capturedIds.has(item.id) ? { ...item, status: "read" } : item),
						unreadCount: feed === target.feed ? 0 : Math.max(0, feed.unreadCount - acknowledged),
					} };
				});
			}
			catch {
				if (!ownsLocal(target)) return;
				setNotice("Couldn't mark every notification read. Refresh and try again.");
			}
		});
		const localReconciliation = Promise.all(localRequests).then(async () => {
			// Start after every local mutation settles so the read cannot race a
			// still-pending local write, or be superseded by a pre-mutation load.
			// Keep server unread facts until then, including when recovery fails.
			if (targets.some(ownsLocal)) await loadRef.current("reconcile");
		});
		const cloudRequest = cloudRead ? cloud.markAllRead().catch(async () => {
			if (cloudRef.current.ownerKey !== ownerKey) return;
			setNotice("Couldn't mark every notification read. Refresh and try again.");
			await cloud.refresh().catch(() => {});
		}) : Promise.resolve();
		await Promise.all([localReconciliation, cloudRequest]);
	}

	useEffect(() => {
		if (!notice) return;
		const timer = setTimeout(() => setNotice(undefined), 2800);
		return () => clearTimeout(timer);
	}, [notice]);

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader
				title="Notifications"
				left={<HeaderIconButton icon="back" label="Back" onPress={() => backOr(router)} />}
				right={
					unreadCount > 0 ? (
						<HeaderIconButton icon="check" label="Mark all read" onPress={() => void markAll()} />
					) : undefined
				}
			/>

			{(loading || cloud.loading) && items.length === 0 ? (
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			) : (
				<SectionList
					sections={sections}
					keyExtractor={(item) => feedKey(item)}
					contentInsetAdjustmentBehavior="automatic"
					contentContainerStyle={
						items.length === 0
							? { flexGrow: 1 }
							: { paddingBottom: insets.bottom + 24 }
					}
					stickySectionHeadersEnabled={false}
					refreshControl={
						<RefreshControl
							refreshing={refreshing || cloud.refreshing}
							onRefresh={() => {
								haptics.tap();
								void refresh();
							}}
							tintColor={t.accent}
						/>
					}
					onEndReached={() => { if (hasMore) { void load("more"); if (cloud.hasMore) void cloud.loadMore().catch(() => {}); } }}
					onEndReachedThreshold={0.4}
					ListHeaderComponent={
						cloud.error && items.length > 0 ? <View style={styles.inlineError}>
							<Feather name="cloud" size={15} color={t.textSecondary} />
							<Text style={styles.inlineErrorText}>Cloud: {cloud.error}</Text>
							<Pressable accessibilityRole="button" accessibilityLabel="Retry Cloud notifications" onPress={() => void cloud.refresh().catch(() => {})}><Text style={styles.inlineErrorText}>Retry</Text></Pressable>
						</View> :
						unavailableFeeds.length > 0 && items.length > 0 ? (
							<View style={styles.inlineError}>
								<Feather name="wifi-off" size={15} color={t.textSecondary} />
								<Text style={styles.inlineErrorText}>
									{unavailableFeeds.length === hostStates.length
										? `${unavailableFeeds.map((feed) => feed.hostName).join(", ")} ${unavailableFeeds.length === 1 ? "is" : "are"} offline. Showing previously loaded activity.`
										: `${unavailableFeeds.map((feed) => feed.hostName).join(", ")} ${unavailableFeeds.length === 1 ? "is" : "are"} unavailable. Showing activity from the available machines.`}
								</Text>
							</View>
						) : null
					}
					renderSectionHeader={({ section }) => (
						section.title
							? <NotificationSectionHeader title={section.title} count={section.data.length} />
							: null
					)}
					renderItem={({ item }) => (
						<NotificationRow
							item={item}
							now={now}
							action={actionFor(item).kind}
							restoring={restoringId === feedKey(item, item.sessionId)}
							onPress={() => open(item)}
							onRestore={() => restoreSession(item)}
							onClear={() => clear(item)}
						/>
					)}
					ListFooterComponent={
						loadingMore || cloud.loadingMore ? (
							<View style={styles.footer}>
								<ActivityIndicator color={t.accent} />
							</View>
						) : null
					}
					ListEmptyComponent={
							cloud.error ? <EmptyState icon="cloud" title="Couldn't load Cloud notifications" message={cloud.error}
								action={<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={() => void refresh()} />} /> :
							!cloud.available && hostStates.length > 0 && unavailableFeeds.length === hostStates.length ? (
							<EmptyState
								icon="wifi-off"
								title="Machines are offline"
								message="Notifications will appear when at least one paired machine reconnects."
								action={<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={() => void load("refresh")} />}
							/>
							) : hostStates.length === 0 && !cloud.available ? (
							// Shared with the tabs: "Connecting…" while the launch race runs,
							// the pairing prompt only once it has found no machine.
							<UnpairedState resolving={!configResolved} />
						) : (
							<EmptyState
								icon="check-circle"
								title="All caught up"
									message="Updates from workers and pull requests across your environments will appear here."
							/>
						)
					}
				/>
			)}

			{/* Sits above the list rather than replacing it: the row that prompted
			    this is still on screen and still has a restore button to press. */}
			{notice ? (
				<View pointerEvents="none" style={[styles.notice, { bottom: insets.bottom + 24 }]}>
					<Feather name="alert-circle" size={15} color={t.amber} />
					<Text style={styles.noticeText}>{notice}</Text>
				</View>
			) : null}
		</View>
	);
}

function NotificationSectionHeader({ title, count }: { title: string; count: number }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.sectionHeader}>
			<Text style={styles.sectionLabel}>{title}</Text>
			<View style={styles.sectionRule} />
			<Text style={styles.sectionCount}>{count}</Text>
		</View>
	);
}

function NotificationRow({ item, now, action, restoring, onPress, onRestore, onClear }: {
	item: FeedItem;
	now: number;
	action: "open" | "review" | "restore" | "prs" | "none";
	restoring: boolean;
	onPress: () => void;
	onRestore: () => void;
	onClear: () => boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const visual = notificationVisual(t, item.type);
	const unread = item.status === "unread";
	const hostName = item.environment === "cloud" ? "Cloud" : item.hostName;
	const appearance = item.environment === "cloud" ? { symbol: "cloud" as const } : machineAppearance({ id: item.hostId, name: hostName, appearance: item.hostAppearance });

	return (
		<NotificationSwipeRow onClear={onClear}>
			<View style={[styles.row, action === "none" && styles.rowInert]}>
				<Pressable
					onPress={onPress}
					accessibilityRole="button"
					accessibilityState={{ disabled: action === "none" }}
					accessibilityLabel={`${item.title || visual.label}, ${visual.label}, ${hostName}${item.environment === "local" ? " machine" : ""}`}
					accessibilityHint={action === "restore" ? "This session is terminated. Use the restore button to bring it back." : undefined}
					accessibilityActions={[{ name: "delete", label: "Clear notification" }]}
					onAccessibilityAction={(event) => {
						if (event.nativeEvent.actionName === "delete") onClear();
					}}
					style={({ pressed }) => [styles.rowTap, pressed && action !== "none" && styles.rowPressed]}
				>
					<View style={styles.rowCopy}>
						<View style={styles.metaRow}>
							<NotificationTypeIcon icon={visual.icon} color={unread ? visual.color : t.textFaint} />
							<Text style={[styles.kind, unread && { color: visual.color }]} numberOfLines={1}>{visual.label}</Text>
							{unread ? <Dot color={t.accent} size={7} /> : null}
							<MachineIdentity
								name={hostName}
								appearance={appearance}
								style={styles.machineIdentity}
							/>
							<Text style={styles.time}>{relativeTime(item.createdAt, now)}</Text>
						</View>
						<Text style={[styles.title, unread && styles.titleUnread]} numberOfLines={1}>{item.title || visual.label}</Text>
						{item.body ? <Text style={styles.body} numberOfLines={1}>{item.body}</Text> : null}
					</View>
				</Pressable>
				{action === "restore" ? (
					<Pressable
						onPress={onRestore}
						disabled={restoring}
						accessibilityRole="button"
						accessibilityLabel={`Restore ${item.title || visual.label}`}
						accessibilityState={{ busy: restoring, disabled: restoring }}
						style={({ pressed }) => [styles.restoreButton, pressed && styles.restorePressed]}
					>
						{restoring ? <ActivityIndicator size="small" color={t.textSecondary} /> : <Feather name="rotate-ccw" size={20} color={t.textSecondary} />}
					</Pressable>
				) : null}
			</View>
		</NotificationSwipeRow>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		inlineError: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			marginHorizontal: space.lg,
			paddingHorizontal: space.md,
			paddingVertical: space.sm,
			borderRadius: 12,
			borderCurve: "continuous",
			backgroundColor: t.bgSubtle,
		},
		inlineErrorText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, flex: 1 },
		sectionHeader: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.lg,
			paddingTop: space.lg,
			paddingBottom: space.xxs,
		},
		sectionLabel: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "500" },
		sectionRule: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: t.borderSubtle },
		sectionCount: { fontFamily: "Geist_600SemiBold",
			color: t.textFaint,
			fontSize: type.caption1.fontSize,
			lineHeight: type.caption1.lineHeight,
			fontWeight: "600",
			fontVariant: ["tabular-nums"],
		},
		machineIdentity: { maxWidth: 112 },
		row: {
			minHeight: 76,
			flexDirection: "row",
			alignItems: "center",
			borderBottomWidth: StyleSheet.hairlineWidth,
			borderBottomColor: t.borderSubtle,
		},
		rowTap: { flex: 1, minWidth: 0, paddingLeft: space.lg, paddingRight: space.sm, paddingVertical: space.sm },
		// Its own column, wide enough to hit without aiming: restoring is the only
		// thing a terminated row can do, and it should not share the row's tap.
		restoreButton: { width: 56, alignSelf: "stretch", alignItems: "center", justifyContent: "center" },
		restorePressed: { backgroundColor: t.bgElevated },
		rowInert: { opacity: 0.55 },
		notice: {
			position: "absolute",
			left: 18,
			right: 18,
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.md,
			paddingVertical: space.md,
			borderRadius: 12,
			borderCurve: "continuous",
			backgroundColor: t.bgElevated,
			borderWidth: StyleSheet.hairlineWidth,
			borderColor: t.borderDefault,
		},
		noticeText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		rowPressed: { backgroundColor: t.bgElevated },
		rowCopy: { flex: 1, gap: space.hair },
		metaRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
		kind: { flexShrink: 1, fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
		time: { fontFamily: "Geist_400Regular",
			color: t.textFaint,
			fontSize: type.caption1.fontSize,
			lineHeight: type.caption1.lineHeight,
			fontVariant: ["tabular-nums"],
			marginLeft: "auto",
		},
		title: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600" },
		titleUnread: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontWeight: "600" },
		body: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		footer: { paddingVertical: space.lg },
	});
