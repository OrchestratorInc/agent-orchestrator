import { useCallback, useMemo, useRef, useState } from "react";
import { ActivityIndicator, RefreshControl, SectionList, StyleSheet, View } from "react-native";
import { useFocusEffect, useLocalSearchParams } from "expo-router";
import { BoardHeaderItems } from "../../../lib/BoardHeaderItems";
import type { Theme } from "../../../lib/theme";
import { haptics } from "../../../lib/haptics";
import { PRCard } from "../../../lib/PRCard";
import { PRFilterDock } from "../../../lib/pr-filter-dock";
import { ProjectSwitcher } from "../../../lib/ProjectSwitcher";
import { collectPRs, prLifecycle, prListSections, type PRListFilter } from "../../../lib/prView";
import { refreshControlColors } from "../../../lib/refreshControlColors";
import { StaleBanner } from "../../../lib/StaleBanner";
import { useApp } from "../../../lib/store";
import { UnpairedState } from "../../../lib/UnpairedState";
import { PR_SUMMARY_REUSE_MS, usePRSummaries, type PRSummarySession } from "../../../lib/usePRSummaries";
import { useBoardFailure } from "../../../lib/useBoardFailure";
import { TabPageScroll } from "../../../lib/TabPageScroll";
import { useTabBarInset } from "../../../lib/useTabBarInset";
import { Button, EmptyState, ListSectionHeader } from "../../../lib/ui";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { space } from "../../../lib/tokens";
import type { HostedSession } from "../../../lib/hostedRows";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

type Filter = PRListFilter;

const DOCK_HEIGHT = 52;
const NO_SUMMARY_TARGETS: PRSummarySession[] = [];

// Drafts are open PRs — they belong in the Open bucket even though the card
// labels them "draft". Before, `state` had already folded draft into "open", so
// the distinction did not exist anywhere.
const inBucket = (filter: Filter, life: ReturnType<typeof prLifecycle>) => {
	if (filter === "all") return true;
	if (filter === "open") return life === "open" || life === "draft";
	return life === "merged";
};

export default function PRsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const tabBarInset = useTabBarInset();
	const { hostId: routeHostId, projectId: routeProjectId } = useLocalSearchParams<{ hostId?: string; projectId?: string }>();
	const { hostStates, allSessions, configForHost, refreshAll, activeProjectId } = useApp();
	const visibleHosts = useMemo(() => routeHostId ? hostStates.filter((host) => host.hostId === routeHostId) : hostStates, [hostStates, routeHostId]);
	const projectFilter = routeHostId ? (routeProjectId ?? "all") : hostStates.length === 1 ? activeProjectId : "all";
	const prs = useMemo(() => {
		// Hosted rows are structurally shared across sync/notification updates.
		// Keep those identities, and deduplicate PRs within each machine only.
		const byHost = new Map<string, HostedSession[]>();
		for (const session of allSessions) {
			if (routeHostId && session.hostId !== routeHostId) continue;
			const rows = byHost.get(session.hostId);
			if (rows) rows.push(session);
			else byHost.set(session.hostId, [session]);
		}
		return [...byHost.values()].flatMap(sessions => collectPRs(sessions).filter(({ session }) => projectFilter === "all" || session.projectId === projectFilter));
	}, [allSessions, routeHostId, projectFilter]);
	const configured = hostStates.length > 0;
	const loading = visibleHosts.some((host) => host.loading);
	const error = visibleHosts.every((host) => host.connection !== "open") ? visibleHosts.find((host) => host.error)?.error : null;
	const notificationsUnread = hostStates.reduce((sum, host) => sum + host.notificationsUnread, 0);
	const [filter, setFilter] = useState<Filter>("open");
	const [refreshing, setRefreshing] = useState(false);

	// Grouped around the user's next action, matching the Workers board rather
	// than presenting a flat stream in daemon order.
	const filtered = useMemo(() => prs.filter(({ pr }) => inBucket(filter, prLifecycle(pr))), [prs, filter]);
	const sections = useMemo(() => prListSections(prs, filter), [prs, filter]);

	// The rich per-PR detail the cards show lives on a separate endpoint, fetched
	// once per session and cached — see usePRSummaries. Pull-to-refresh is the
	// only thing that re-fetches it.
	const summaryTargets = useMemo(() => filtered.flatMap(({ session }) => {
		const config = configForHost(session.hostId);
		return config ? [{ config, sessionId: session.id }] : [];
	}), [filtered, configForHost]);
	// Every tab mounts with the board, so the details wait for this tab's first
	// visit instead of loading behind Workers at launch.
	const [visited, setVisited] = useState(false);
	const summaries = usePRSummaries(visited ? summaryTargets : NO_SUMMARY_TARGETS);
	const lastSummaryRefresh = useRef(0);
	useFocusEffect(useCallback(() => {
		if (!visited) {
			lastSummaryRefresh.current = Date.now();
			setVisited(true);
			return;
		}
		// Retained tabs should not keep old detail indefinitely, nor restart N
		// requests on every quick tab switch.
		if (Date.now() - lastSummaryRefresh.current < PR_SUMMARY_REUSE_MS) return;
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
	}, [visited, summaries.reload]));
	const failure = useBoardFailure();

	const onRefresh = async () => {
		haptics.tap();
		setRefreshing(true);
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
		await refreshAll();
		setRefreshing(false);
	};

	if (!configured) {
		return (
			<View style={styles.screen}>
				<TabPageScroll>
					<UnpairedState />
				</TabPageScroll>
				<BoardHeaderItems unread={0} notifications={false} />
			</View>
		);
	}

	// The filter floats just above the tab bar.
	const dockBottom = Math.max(tabBarInset, space.md);
	const counts = {
		open: prs.filter((p) => inBucket("open", prLifecycle(p.pr))).length,
		merged: prs.filter((p) => prLifecycle(p.pr) === "merged").length,
		all: prs.length,
	};

	// In the list, not above it: the list has to be the page's first view under
	// the translucent header.
	const listHeader = (
		<>
			{hostStates.length === 1 && !routeHostId ? <ProjectSwitcher /> : null}
			{hostStates.length === 1 ? <StaleBanner error={!!error} onRetry={onRefresh} /> : null}
		</>
	);

	return (
		<View style={styles.screen}>
			{loading && prs.length === 0 ? (
				<TabPageScroll>
					{listHeader}
					<View style={styles.center}>
						<ActivityIndicator color={t.accent} />
					</View>
				</TabPageScroll>
			) : (
				<SectionList
					sections={sections}
					keyExtractor={({ pr, session }) => `${session.hostId}:${session.projectId}#${pr.number}`}
					contentInsetAdjustmentBehavior="automatic"
					// iOS insets the list by the tab bar already; it pads only for the filter.
					contentContainerStyle={{ flexGrow: 1, paddingBottom: dockBottom - tabBarInset + DOCK_HEIGHT + space.xxl }}
					stickySectionHeadersEnabled={false}
					ListHeaderComponent={listHeader}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} {...refreshControlColors(t)} />}
					renderSectionHeader={({ section }) => <ListSectionHeader label={section.label} />}
					renderItem={({ item: { pr, session } }) => {
						const config = configForHost(session.hostId);
						return <PRCard
							pr={pr}
							session={session}
							hostId={session.hostId}
							hostName={hostStates.length > 1 ? session.hostName : undefined}
							summary={config ? summaries.summaryFor(config, session.id, pr.number) : undefined}
						/>;
					}}
					ListEmptyComponent={
						filtered.length === 0 ? (
							error ? (
								<EmptyState
									icon={failure.icon}
									title={failure.title}
									message={failure.hint}
									action={<Button title="Retry" icon="refresh-cw" variant="secondary" onPress={onRefresh} />}
								/>
							) : (
								<EmptyState
									icon="git-pull-request"
									title="No pull requests"
									message={filter === "open" ? "No open PRs right now." : "Nothing here yet."}
								/>
							)
						) : null
					}
				/>
			)}

			<View style={[styles.dock, { bottom: dockBottom }]}>
				<PRFilterDock filter={filter} counts={counts} onChange={setFilter} />
			</View>
			<BoardHeaderItems unread={notificationsUnread} />
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center" },
		dock: {
			position: "absolute",
			left: 16,
			right: 16,
			height: DOCK_HEIGHT,
			flexDirection: "row",
			alignItems: "center",
			justifyContent: "center",
			gap: space.sm,
		},
	});
