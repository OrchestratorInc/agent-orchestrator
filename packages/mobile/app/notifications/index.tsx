import { Stack, useFocusEffect, type Href } from "expo-router";
import { useIsFocused } from "expo-router/react-navigation";
import { useOpenPage } from "../../lib/pageNavigation";
import { StackRootBack } from "../../lib/StackRootBack";
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
	clearAllNotifications,
	clearNotification,
	getNotifications,
	markAllNotificationsRead,
	markNotificationRead,
	type NotificationRecord,
} from "../../lib/api";
import { AppSymbol } from "../../lib/AppSymbol";
import { rowDividerWidth } from "../../lib/divider";
import { haptics } from "../../lib/haptics";
import { NotificationTypeIcon } from "../../lib/notification-type-icon";
import {
	notificationIdentity,
	notificationsAcrossHosts,
	notificationSections,
	notificationAction,
	notificationTarget,
	notificationVisual,
	relativeTime,
	type HostedNotification,
} from "../../lib/notificationView";
import { useApp } from "../../lib/store";
import { MINUTE_MS, useNow } from "../../lib/useNow";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { Button, Dot, EmptyState, ListSectionHeader } from "../../lib/ui";
import { usePressFeedback } from "../../lib/pressFeedback";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { toolbarIcon } from "../../lib/toolbarIcon";
import { UnpairedState } from "../../lib/UnpairedState";
import { iconSize, radius, space, stateOpacity, type } from "../../lib/tokens";
import { userFacingError } from "../../lib/connectionError";
import { ToastHost } from "../../lib/Toast";
import type { ToastDuration } from "../../lib/Toast.types";
import { useToast } from "../../lib/useToast";
import { NotificationSwipeRow } from "../../lib/NotificationSwipeRow";
import { MachineIdentity } from "../../lib/MachineIdentity";
import { machineAppearance } from "../../lib/machineAppearance";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

const PAGE_SIZE = 50;
type FeedItem = HostedNotification<NotificationRecord>;
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
	const openPage = useOpenPage();
	const insets = useSafeAreaInsets();
	const { hostStates, configForHost, refreshHost, restore } = useApp();
	const [restoringId, setRestoringId] = useState<string>();
	// A brief line rather than an Alert: the row is still there to act on, and
	// a modal would make a dead tap feel like an error. Every one of these says
	// why something did not happen, so they all carry the warning tone.
	const toast = useToast();
	const showToast = toast.show;
	const notify = useCallback((text: string, duration?: ToastDuration) => showToast(text, { tone: "warning", duration }), [showToast]);
	const now = useNow(MINUTE_MS);
	const [feeds, setFeeds] = useState<Record<string, HostFeed>>({});
	const feedsRef = useRef(feeds);
	feedsRef.current = feeds;
	const hostsRef = useRef(hostStates);
	hostsRef.current = hostStates;
	const loadGeneration = useRef(0);
	const loadingMoreRef = useRef(false);
	const loadedOnceRef = useRef(false);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [loadingMore, setLoadingMore] = useState(false);
	// Opening the screen marks what it loaded as read, as the desktop panel does.
	// Those rows keep their unread look for this visit so the user can still see
	// what was new.
	const [highlightedIds, setHighlightedIds] = useState<Set<string>>(() => new Set());
	const acknowledgedRef = useRef(new Set<string>());
	// Which ack attempt last claimed each row. A reload re-acks the same rows, so
	// an older attempt that fails late must not roll back a newer one.
	const ackOwnerRef = useRef(new Map<string, number>());
	const ackAttemptRef = useRef(0);
	// A visit ends when the screen loses focus, not when it unmounts: tapping into
	// a session leaves this screen mounted underneath, and coming back should
	// show those rows as read, like reopening the desktop panel.
	useFocusEffect(useCallback(() => () => setHighlightedIds(new Set()), []));
	const focused = useIsFocused();
	const items = useMemo(() => notificationsAcrossHosts(Object.values(feeds).map((feed) => {
		const host = hostStates.find((candidate) => candidate.hostId === feed.hostId);
		return {
			hostId: feed.hostId,
			hostName: feed.hostName,
			hostAppearance: machineAppearance({ id: feed.hostId, name: feed.hostName, appearance: host?.appearance }),
			items: feed.items,
		};
	})), [feeds, hostStates]);
	const sections = useMemo(() => notificationSections(items), [items]);
	const unavailableFeeds = useMemo(() => Object.values(feeds).filter((feed) => feed.error), [feeds]);
	const hasMore = useMemo(() => Object.values(feeds).some((feed) => Boolean(feed.nextCursor)), [feeds]);
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
		async (mode: "initial" | "refresh" | "more") => {
			if (mode === "more" && loadingMoreRef.current) return;
			const hosts = hostsRef.current;
			const currentFeeds = feedsRef.current;
			const generation = mode === "more" ? loadGeneration.current : ++loadGeneration.current;
			// A reload may bring back rows whose ack failed; let them be retried.
			if (mode !== "more") acknowledgedRef.current = new Set();
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
					const priorItems = mode === "more" ? previousFeed?.items ?? [] : [];
					const seen = new Set(priorItems.map((item) => item.id));
					next[result.host.hostId] = {
						hostId: result.host.hostId,
						hostName: result.host.name,
						items: [...priorItems, ...result.page.notifications.filter((item) => !seen.has(item.id))],
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

	useEffect(() => {
		void load("initial");
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [hostSignature]);

	// Concrete loaded ids only, never "everything": unread rows on pages not
	// loaded yet stay unread until the user scrolls to them. Only while focused:
	// a reload under an open session (a host reconnecting) must not mark rows
	// the user has not seen. They are acked, and highlighted, on return.
	useEffect(() => {
		if (!focused) return;
		for (const feed of Object.values(feeds)) {
			const source = feed.error ? null : configForHost(feed.hostId);
			if (!source) continue;
			const ids = feed.items
				.filter((item) => item.status === "unread" && !acknowledgedRef.current.has(notificationIdentity(feed.hostId, item.id)))
				.map((item) => item.id);
			if (ids.length === 0) continue;
			const keys = ids.map((id) => notificationIdentity(feed.hostId, id));
			const attempt = ++ackAttemptRef.current;
			for (const key of keys) {
				acknowledgedRef.current.add(key);
				ackOwnerRef.current.set(key, attempt);
			}
			setHighlightedIds((current) => new Set([...current, ...keys]));
			const acked = new Set(ids);
			updateFeeds((previous) => {
				const current = previous[feed.hostId];
				if (!current) return previous;
				const marked = current.items.filter((item) => acked.has(item.id) && item.status === "unread").length;
				return { ...previous, [feed.hostId]: {
					...current,
					items: current.items.map((item) => acked.has(item.id) ? { ...item, status: "read" } : item),
					unreadCount: Math.max(0, current.unreadCount - marked),
				} };
			});
			markAllNotificationsRead(source, ids).then(
				// Refresh that machine so the bell badge is already clear on the way back.
				// Its failure says nothing about the ack, so it must not roll rows back.
				() => { void refreshHost(feed.hostId).catch(() => {}); },
				() => {
					// Put back to unread only the rows this attempt still owns, without
					// forgetting the ack, so a machine that keeps refusing is not retried
					// in a loop. Pull-to-refresh retries.
					const owned = new Set(ids.filter((id) => ackOwnerRef.current.get(notificationIdentity(feed.hostId, id)) === attempt));
					if (owned.size === 0) return;
					notify(`Couldn't mark notifications read on ${feed.hostName}.`);
					updateFeeds((previous) => {
						const current = previous[feed.hostId];
						if (!current) return previous;
						const restored = current.items.filter((item) => owned.has(item.id) && item.status === "read").length;
						return { ...previous, [feed.hostId]: {
							...current,
							items: current.items.map((item) => owned.has(item.id) ? { ...item, status: "unread" } : item),
							unreadCount: current.unreadCount + restored,
						} };
					});
				},
			);
		}
	}, [configForHost, feeds, focused, notify, refreshHost, updateFeeds]);

	function open(notification: FeedItem) {
		const source = configForHost(notification.hostId);
		if (!source) {
			haptics.warning();
			notify(`${notification.hostName} is offline.`);
			return;
		}
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
		// Through openPage, which dismisses any sheet still up before pushing.
		if (action.kind === "open") openPage({ pathname: "/session/[id]", params: { id: action.sessionId, hostId: notification.hostId } });
		else if (action.kind === "review") openPage(notificationTarget(notification, knownHostIds) as Href);
		else if (action.kind === "prs") openPage({ pathname: "/prs", params: { hostId: notification.hostId } });
		else if (action.kind === "restore") {
			haptics.warning();
			notify("This session is terminated. Tap restore to bring it back.");
		} else if (action.kind === "none") {
			haptics.warning();
			notify("That session is not available yet.");
		}
	}

	function clear(notification: FeedItem) {
		const source = configForHost(notification.hostId);
		if (!source) {
			haptics.warning();
			notify(`${notification.hostName} is offline.`);
			return false;
		}
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
			notify(userFacingError(cause, "Couldn't clear that notification."));
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
			// and guessing lands on a screen that cannot resolve it. A machine that
			// pages its archive has answered once it reports the archive's size,
			// even with no live workers. Its archived workers are not in `sessions`,
			// so one of those offers Open, whose screen offers Restore.
			sessionsReady: Boolean(host && !host.loading && (host.sessions.length > 0 || host.archive)),
		};
	}

	function restoreSession(hostId: string, sessionId: string) {
		const key = notificationIdentity(hostId, sessionId);
		setRestoringId(key);
		void restore(sessionId, hostId)
			.then(() => {
				haptics.success();
				openPage({ pathname: "/session/[id]", params: { id: sessionId, hostId } });
			})
			.catch((cause) => {
				haptics.error();
				Alert.alert("Couldn't restore the session", userFacingError(cause));
			})
			.finally(() => setRestoringId(undefined));
	}

	function confirmClearAll() {
		haptics.warning();
		Alert.alert("Clear all notifications?", "This removes the notification history on every connected machine.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Clear all", style: "destructive", onPress: () => void clearAll() },
		]);
	}

	// Mirrors the desktop panel's Clear all: each machine's whole history goes,
	// including pages this screen never loaded.
	async function clearAll() {
		const feedList = Object.values(feedsRef.current);
		const targets = feedList.flatMap((feed) => {
			const source = configForHost(feed.hostId);
			return source ? [{ feed, source }] : [];
		});
		const offline = feedList.filter((feed) => !configForHost(feed.hostId) && feed.items.length > 0);
		if (targets.length === 0) {
			haptics.warning();
			notify("Notifications are on machines that are currently offline.");
			return;
		}
		// Drop anything in flight so a page that lands now cannot bring rows back.
		loadGeneration.current += 1;
		// Each emptied feed is kept by identity, so a failed clear can put the old
		// rows back without overwriting anything a reload has written since. The
		// recovery reload below may fail too, and it keeps whatever feed it finds.
		const cleared = new Map(targets.map(({ feed }) => [feed.hostId, { ...feed, items: [], nextCursor: undefined, unreadCount: 0 }]));
		updateFeeds((previous) => Object.fromEntries(Object.entries(previous).map(([hostId, feed]) => [hostId, cleared.get(hostId) ?? feed])));
		const results = await Promise.allSettled(targets.map(async ({ feed, source }) => {
			try {
				await clearAllNotifications(source);
			} catch (cause) {
				updateFeeds((previous) => previous[feed.hostId] === cleared.get(feed.hostId) ? { ...previous, [feed.hostId]: feed } : previous);
				throw new Error(userFacingError(cause, `Couldn't clear notifications on ${feed.hostName}.`));
			}
			void refreshHost(feed.hostId);
		}));
		const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
		if (failure) {
			haptics.error();
			notify(failure.reason instanceof Error ? failure.reason.message : "Couldn't clear notifications.");
		} else if (offline.length > 0) {
			notify(`${offline.map((feed) => feed.hostName).join(", ")} ${offline.length === 1 ? "is" : "are"} offline, so ${offline.length === 1 ? "its" : "their"} notifications were not cleared.`, "long");
		}
		// Also releases pagination that the generation bump cut off.
		void load("refresh");
	}

	// Memoized on whether there is anything to clear: the screen re-renders on
	// every poll, and each new toolbar element registers the header's options
	// again. The item calls the latest confirm, whose clear reads this render's
	// machines.
	const confirmClearAllRef = useRef(confirmClearAll);
	confirmClearAllRef.current = confirmClearAll;
	const clearable = items.length > 0;
	// A pushed page on both platforms: the bar's back button leaves it, and
	// Clear all takes the trailing end.
	const headerItems = useMemo(() => (
		clearable ? (
			<Stack.Toolbar placement="right">
				<Stack.Toolbar.Button icon={toolbarIcon("delete")} accessibilityLabel="Clear all" onPress={() => confirmClearAllRef.current()} />
			</Stack.Toolbar>
		) : null
	), [clearable]);

	return (
		// Not collapsable, as in Settings: flattened away, it leaves the list a
		// direct child of the sheet, which react-native-screens then resizes
		// wrongly and the page shows blank under its bar.
		<View style={styles.screen} collapsable={false}>
			<StackRootBack />
			{headerItems}
			{/* The list is the page's first view: on iOS 26 the clear bar blurs it
			    as it scrolls under. It is the only scroll view the page ever has,
			    the first load included. Swapping a stand-in scroll view for it
			    while the sheet came up left iOS 26's floating bar over the sheet
			    taking every touch, so neither the close button nor the list
			    responded. */}
			<SectionList
				sections={sections}
				keyExtractor={(item) => notificationIdentity(item.hostId, item.id)}
				contentInsetAdjustmentBehavior="automatic"
				contentContainerStyle={
					items.length === 0
						? { flexGrow: 1 }
						: { paddingBottom: insets.bottom + 24 }
				}
				stickySectionHeadersEnabled={false}
				refreshControl={
					<RefreshControl
						refreshing={refreshing}
						// The refresh control gives each platform's own feedback.
						onRefresh={() => void load("refresh")}
						{...refreshControlColors(t)}
					/>
				}
				onEndReached={() => { if (hasMore) void load("more"); }}
				onEndReachedThreshold={0.4}
				ListHeaderComponent={
					unavailableFeeds.length > 0 && items.length > 0 ? (
						<View style={styles.inlineError}>
							<AppSymbol name="offline" size={iconSize.sm} color={t.textSecondary} />
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
						? <ListSectionHeader label={section.title} count={section.data.length} />
						: null
				)}
				renderItem={({ item }) => (
					<NotificationRow
						item={item}
						highlighted={highlightedIds.has(notificationIdentity(item.hostId, item.id))}
						now={now}
						action={notificationAction(item, sessionState(item.hostId, item.sessionId)).kind}
						restoring={restoringId === notificationIdentity(item.hostId, item.sessionId)}
						onPress={() => open(item)}
						onRestore={() => item.sessionId && restoreSession(item.hostId, item.sessionId)}
						onClear={() => clear(item)}
					/>
				)}
				ListFooterComponent={
					loadingMore ? (
						<View style={styles.footer}>
							<ActivityIndicator color={t.accent} />
						</View>
					) : null
				}
				ListEmptyComponent={
					loading ? (
						<View style={styles.center}>
							<ActivityIndicator color={t.accent} />
						</View>
					) : hostStates.length > 0 && unavailableFeeds.length === hostStates.length ? (
						<EmptyState
							icon="wifi-off"
							title="Machines are offline"
							message="Notifications will appear when at least one paired machine reconnects."
							action={<Button title="Retry" icon="refresh-cw" onPress={() => void load("refresh")} />}
						/>
					) : hostStates.length === 0 ? (
						// Shared with the tabs: "Connecting…" while the launch race runs,
						// the pairing prompt only once it has found no machine.
						<UnpairedState />
					) : (
						<EmptyState
							icon="check-circle"
							title="All caught up"
							message="Updates from workers and pull requests across your machines will appear here when they need you."
						/>
					)
				}
			/>

			{/* Sits above the list rather than replacing it: the row that prompted
			    this is still on screen and still has a restore button to press. */}
			<ToastHost toast={toast} />
		</View>
	);
}

function NotificationRow({ item, highlighted, now, action, restoring, onPress, onRestore, onClear }: {
	item: FeedItem;
	highlighted: boolean;
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
	const unread = item.status === "unread" || highlighted;
	const inert = action === "none";
	const rowFeedback = usePressFeedback("row");
	const restoreFeedback = usePressFeedback("icon");

	return (
		<NotificationSwipeRow onClear={onClear}>
			<View style={[styles.row, inert && styles.rowInert]}>
				<Pressable
					onPress={onPress}
					accessibilityRole="button"
					accessibilityState={{ disabled: inert }}
					// The dot and the heavier title say "unread" to the eye; this says it aloud.
					accessibilityLabel={`${unread ? "Unread, " : ""}${item.title || visual.label}, ${visual.label}, ${item.hostName} machine`}
					accessibilityHint={action === "restore" ? "This session is terminated. Use the restore button to bring it back." : undefined}
					accessibilityActions={[{ name: "delete", label: "Clear notification" }]}
					onAccessibilityAction={(event) => {
						if (event.nativeEvent.actionName === "delete") onClear();
					}}
					android_ripple={inert ? undefined : rowFeedback.android_ripple}
					style={({ pressed }) => [styles.rowTap, pressed && !inert && rowFeedback.pressed]}
				>
					<View style={styles.rowCopy}>
						<View style={styles.metaRow}>
							<NotificationTypeIcon icon={visual.icon} color={unread ? visual.color : t.textFaint} />
							<Text style={[styles.kind, unread && { color: visual.color }]} numberOfLines={1}>{visual.label}</Text>
							{unread ? <Dot color={t.accent} size={7} /> : null}
							<MachineIdentity
								name={item.hostName}
								appearance={machineAppearance({ id: item.hostId, name: item.hostName, appearance: item.hostAppearance })}
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
						android_ripple={restoreFeedback.android_ripple}
						style={({ pressed }) => [styles.restoreButton, pressed && restoreFeedback.pressed]}
					>
						{restoring
							? <ActivityIndicator size="small" color={t.textSecondary} />
							: <AppSymbol name={{ ios: "arrow.counterclockwise", android: "restore" }} size={iconSize.lg} color={t.textSecondary} />}
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
			borderRadius: radius.md,
			borderCurve: "continuous",
			backgroundColor: t.bgSubtle,
		},
		inlineErrorText: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, flex: 1 },
		machineIdentity: { maxWidth: 112 },
		row: {
			minHeight: 76,
			flexDirection: "row",
			alignItems: "center",
			borderBottomWidth: rowDividerWidth,
			borderBottomColor: t.borderSubtle,
		},
		rowTap: { flex: 1, minWidth: 0, paddingLeft: space.lg, paddingRight: space.sm, paddingVertical: space.sm },
		// Its own column, wide enough to hit without aiming: restoring is the only
		// thing a terminated row can do, and it should not share the row's tap.
		restoreButton: { width: 56, alignSelf: "stretch", alignItems: "center", justifyContent: "center" },
		rowInert: { opacity: stateOpacity.disabled },
		rowCopy: { flex: 1, gap: space.hair },
		metaRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
		kind: { flexShrink: 1, fontFamily: "DMSans_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
		time: { fontFamily: "DMSans_400Regular",
			color: t.textFaint,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			fontVariant: ["tabular-nums"],
			marginLeft: "auto",
		},
		title: { fontFamily: "DMSans_600SemiBold", color: t.textSecondary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600" },
		titleUnread: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontWeight: "600" },
		body: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		footer: { paddingVertical: space.lg },
	});
