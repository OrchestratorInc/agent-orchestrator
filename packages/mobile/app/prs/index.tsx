import { useCallback, useMemo, useRef, useState } from "react";
import { Platform, RefreshControl, SectionList, StyleSheet, View } from "react-native";
import { useFocusEffect, useLocalSearchParams } from "expo-router";
import type { Theme } from "../../lib/theme";
import { PRCard } from "../../lib/PRCard";
import { PRFilterControl } from "../../lib/pr-filter-control";
import { ProjectSwitcher } from "../../lib/ProjectSwitcher";
import { collectPRs, prFilterCounts, prInFilter, prLifecycle, prListSections, type PRListFilter } from "../../lib/prView";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { Spinner } from "../../lib/Spinner";
import { StackRootBack } from "../../lib/StackRootBack";
import { StaleBanner } from "../../lib/StaleBanner";
import { useApp } from "../../lib/store";
import { UnpairedState } from "../../lib/UnpairedState";
import { PR_SUMMARY_REUSE_MS, usePRSummaries } from "../../lib/usePRSummaries";
import { useBoardFailure } from "../../lib/useBoardFailure";
import { TabPageScroll } from "../../lib/TabPageScroll";
import { ToastHost } from "../../lib/Toast";
import { useToast } from "../../lib/useToast";
import { Button, EmptyState, ListSectionHeader } from "../../lib/ui";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { space } from "../../lib/tokens";
import type { HostedSession } from "../../lib/hostedRows";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

type Filter = PRListFilter;

type PRsParams = { hostId?: string; projectId?: string };

export default function PRsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { hostId: routeHostId, projectId: routeProjectId } = useLocalSearchParams<PRsParams>();
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
	const [filter, setFilter] = useState<Filter>("open");
	const [refreshing, setRefreshing] = useState(false);
	const toast = useToast();
	const { show: showToast } = toast;
	// Android 13 and later confirms every copy with its own clipboard preview.
	const linkCopied = useCallback(() => {
		if (Platform.OS === "android" && Number(Platform.Version) >= 33) return;
		showToast("Link copied.");
	}, [showToast]);

	// Grouped around the user's next action, matching the Workers board rather
	// than presenting a flat stream in daemon order.
	const filtered = useMemo(() => prs.filter(({ pr }) => prInFilter(filter, prLifecycle(pr))), [prs, filter]);
	const sections = useMemo(() => prListSections(prs, filter), [prs, filter]);
	const counts = useMemo(() => prFilterCounts(prs), [prs]);

	// The rich per-PR detail the cards show lives on a separate endpoint, fetched
	// once per session and cached — see usePRSummaries. Pull-to-refresh is the
	// only thing that re-fetches it.
	const summaryTargets = useMemo(() => filtered.flatMap(({ session }) => {
		const config = configForHost(session.hostId);
		return config ? [{ config, sessionId: session.id }] : [];
	}), [filtered, configForHost]);
	const summaries = usePRSummaries(summaryTargets);
	// The page loads its details as it opens. Coming back to it from a PR or a
	// worker reloads them once they are old, not on every return.
	const lastSummaryRefresh = useRef(Date.now());
	useFocusEffect(useCallback(() => {
		if (Date.now() - lastSummaryRefresh.current < PR_SUMMARY_REUSE_MS) return;
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
	}, [summaries.reload]));
	const failure = useBoardFailure();

	// No haptic of its own: each platform's refresh control gives the feedback
	// it gives everywhere, and the Retry buttons that also call this buzz once.
	const onRefresh = async () => {
		setRefreshing(true);
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
		await refreshAll();
		setRefreshing(false);
	};

	if (!configured) {
		return (
			<View style={styles.screen} collapsable={false}>
				<StackRootBack />
				<TabPageScroll>
					<UnpairedState />
				</TabPageScroll>
			</View>
		);
	}

	// In the list, not above it: the list has to be the page's first view under
	// the translucent header. The filter heads it, under the title, where each
	// platform puts a segmented control.
	const listHeader = (
		<>
			{hostStates.length === 1 && !routeHostId ? <ProjectSwitcher /> : null}
			<PRFilterControl filter={filter} counts={counts} onChange={setFilter} />
			{hostStates.length === 1 ? <StaleBanner error={!!error} onRetry={onRefresh} /> : <StaleBanner networkOnly />}
		</>
	);

	return (
		// Not collapsable, as in Notifications: flattened away, it leaves the list
		// a direct child of the sheet, which react-native-screens then resizes
		// wrongly and the page shows blank under its bar.
		<View style={styles.screen} collapsable={false}>
			<StackRootBack />
			{/* The only scroll view the page has, the first load included: swapping
			    a stand-in for it while the sheet came up left iOS 26's floating bar
			    over the sheet taking every touch. */}
			<SectionList
				sections={sections}
				keyExtractor={({ pr, session }) => `${session.hostId}:${session.projectId}#${pr.number}`}
				contentInsetAdjustmentBehavior="automatic"
				contentContainerStyle={{ flexGrow: 1, paddingBottom: space.xxl }}
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
						onLinkCopied={linkCopied}
					/>;
				}}
				ListEmptyComponent={
					loading && prs.length === 0 ? (
						<View style={styles.center}>
							<Spinner color={t.accent} accessibilityLabel="Loading pull requests" />
						</View>
					) : filtered.length === 0 ? (
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

			<ToastHost toast={toast} />
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// No background of its own: the stack's contentStyle paints the page. A
		// coloured view that is not a stacking context is mounted empty with its
		// children hoisted after it, which put it ahead of the list as the page's
		// first native view, and the bar found no scroll view to inset it for.
		screen: { flex: 1 },
		center: { flex: 1, alignItems: "center", justifyContent: "center" },
	});
