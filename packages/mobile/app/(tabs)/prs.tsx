import { useCallback, useMemo, useRef, useState } from "react";
import { ActivityIndicator, RefreshControl, SectionList, StyleSheet, Text, View } from "react-native";
import { useFocusEffect, useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { Theme } from "../../lib/theme";
import { haptics } from "../../lib/haptics";
import { PRCard } from "../../lib/PRCard";
import { PRFilterDock } from "../../lib/pr-filter-dock";
import { collectPRs, prLifecycle, prListSections, type PRListFilter } from "../../lib/prView";
import { ProjectSwitcher } from "../../lib/ProjectSwitcher";
import { StaleBanner } from "../../lib/StaleBanner";
import { useApp } from "../../lib/store";
import { UnpairedState } from "../../lib/UnpairedState";
import { PR_SUMMARY_REUSE_MS, usePRSummaries } from "../../lib/usePRSummaries";
import { useBoardFailure } from "../../lib/useBoardFailure";
import { useTabScrollToTop } from "../../lib/useTabScrollToTop";
import { useNotificationBadge } from "../../lib/useNotificationBadge";
import { Button, EmptyState, HeaderIconButton, ListSectionHeader, ScreenHeader } from "../../lib/ui";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { space } from "../../lib/tokens";
import type { HostedSession } from "../../lib/hostedRows";
import { sourceKey } from "../../lib/environment/scopedBoard";
import { ALL_PROJECTS, resolveActiveProject } from "../../lib/projectFilter";
import { useSourcePRs } from "../../lib/pr/useSourcePRs";
import type { DashboardPR } from "../../lib/api";
import { routeSource } from "../../lib/session/sessionRoute";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

type Filter = PRListFilter;

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
	const insets = useSafeAreaInsets();
	const router = useRouter();
	const params = useLocalSearchParams<{ hostId?: string; projectId?: string; source?: string; sourceId?: string }>();
	const { hostId: routeHostId, projectId: routeProjectId } = params;
	const explicitSource = routeSource(params);
	const invalidSource = routeHostId !== undefined && (typeof routeHostId !== "string" || !routeHostId.trim()) || explicitSource?.kind === "invalid" || Boolean(routeHostId && explicitSource && (explicitSource.kind !== "local" || explicitSource.id !== routeHostId));
	const { hostStates, allSessions, configForHost, refreshAll, refreshSource, environment, activeProjectId, configResolved, localConfigured, scopedBoard, sourceFor, availableSources, cloudBoard } = useApp();
	const cloudTargets = useMemo(() => (scopedBoard?.sessions ?? []).flatMap(({ source, value }) => {
		if (invalidSource || routeHostId || source.kind !== "cloud" || value.kind === "orchestrator" || explicitSource && (explicitSource.kind !== "cloud" || explicitSource.id !== source.id) || routeProjectId && routeProjectId !== "all" && value.projectId !== routeProjectId) return [];
		const adapter = sourceFor?.(source)?.pullRequests;
		return adapter ? [{ source: adapter, sessionId: value.id, ref: source, session: value }] : [];
	}), [scopedBoard?.sessions, sourceFor, routeHostId, routeProjectId, params.source, params.sourceId, invalidSource]);
	const cloudPRs = useSourcePRs(cloudTargets);
	const cloudRows = useMemo(() => cloudTargets.flatMap((target) => cloudPRs.list(target.source, target.sessionId).map((summary) => ({
		...target, summary,
		session: target.session,
		pr: { number: summary.number, url: summary.url, title: summary.title, state: summary.state === "draft" ? "open" : summary.state, isDraft: summary.state === "draft", repo: summary.repo, ciStatus: summary.ci.state, reviewDecision: summary.review.decision, unresolvedThreads: summary.review.hasUnresolvedHumanComments ? 1 : 0 } as DashboardPR,
	}))), [cloudTargets, cloudPRs.version]);
	const visibleHosts = useMemo(() => explicitSource?.kind === "cloud" ? [] : routeHostId ? hostStates.filter((host) => host.hostId === routeHostId) : hostStates, [hostStates, routeHostId, params.source]);
	const hostIdsKey = JSON.stringify(hostStates.map((host) => host.hostId));
	const localSources = useMemo(() => new Map(hostStates.map((host) => [host.hostId, { kind: "local" as const, id: host.hostId }])), [hostIdsKey]);
	const singleHost = explicitSource?.kind !== "cloud" && !routeHostId && hostStates.length === 1 ? hostStates[0] : undefined;
	const [projectSelection, setProjectSelection] = useState<{ hostId: string; projectId: string } | null>(null);
	// A retained Cloud project is never the selected project on a Local host.
	const selectedProject = singleHost && projectSelection?.hostId === singleHost.hostId
		? projectSelection.projectId : environment === "local" ? activeProjectId : ALL_PROJECTS;
	const projectFilter = routeHostId ? (routeProjectId ?? ALL_PROJECTS) : singleHost
		? resolveActiveProject(selectedProject, singleHost.projects, singleHost.projectsKnown) : ALL_PROJECTS;
	const localPRs = useMemo(() => {
		// Hosted rows are structurally shared across sync/notification updates.
		// Keep those identities, and deduplicate PRs within each machine only.
		const byHost = new Map<string, HostedSession[]>();
		for (const session of allSessions) {
			if (invalidSource || explicitSource?.kind === "cloud" || explicitSource?.kind === "local" && session.hostId !== explicitSource.id) continue;
			if (routeHostId && session.hostId !== routeHostId) continue;
			const rows = byHost.get(session.hostId);
			if (rows) rows.push(session);
			else byHost.set(session.hostId, [session]);
		}
		return [...byHost.values()].flatMap(sessions => collectPRs(sessions).filter(({ session }) => projectFilter === "all" || session.projectId === projectFilter));
	}, [allSessions, routeHostId, projectFilter, invalidSource, params.source, params.sourceId]);
	const prs = useMemo(() => {
		const seen = new Set<string>();
		const cloud = cloudRows.filter((row) => { const key = JSON.stringify([row.source.scopeKey, row.pr.url]); if (seen.has(key)) return false; seen.add(key); return true; });
		return [...localPRs, ...cloud];
	}, [localPRs, cloudRows]);
	const cloudAvailable = !routeHostId && !invalidSource && (availableSources ?? []).some((source) => source.kind === "cloud" && (!explicitSource || explicitSource.kind === "cloud" && explicitSource.id === source.id));
	const loading = visibleHosts.some((host) => host.loading) || cloudPRs.loading || cloudAvailable && cloudBoard?.loading;
	const error = cloudPRs.error || (cloudAvailable ? cloudBoard?.error : null) || (visibleHosts.every((host) => host.connection !== "open") ? visibleHosts.find((host) => host.error)?.error : null);
	const notifications = useNotificationBadge();
	const [filter, setFilter] = useState<Filter>("open");
	const [refreshing, setRefreshing] = useState(false);

	const scrollRef = useTabScrollToTop<SectionList>();

	// Grouped around the user's next action, matching the Workers board rather
	// than presenting a flat stream in daemon order.
	const filtered = useMemo(() => prs.filter(({ pr }) => inBucket(filter, prLifecycle(pr))), [prs, filter]);
	const sections = useMemo(() => prListSections(prs, filter), [prs, filter]);

	// The rich per-PR detail the cards show lives on a separate endpoint, fetched
	// once per session and cached — see usePRSummaries. Pull-to-refresh is the
	// only thing that re-fetches it.
	const summaryTargets = useMemo(() => localPRs.flatMap(({ session }) => {
		const config = configForHost(session.hostId);
		return config ? [{ config, sessionId: session.id }] : [];
	}), [localPRs, configForHost]);
	const summaries = usePRSummaries(summaryTargets);
	const lastSummaryRefresh = useRef(Date.now());
	useFocusEffect(useCallback(() => {
		// Retained tabs should not keep old detail indefinitely, nor restart N
		// requests on every quick sidebar switch.
		if (Date.now() - lastSummaryRefresh.current < PR_SUMMARY_REUSE_MS) return;
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
		cloudPRs.reload();
	}, [summaries.reload, cloudPRs.reload]));
	const failure = useBoardFailure();

	const onRefresh = async () => {
		haptics.tap();
		setRefreshing(true);
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
		cloudPRs.reload();
		try {
			if (routeHostId) await refreshSource({ kind: "local", id: routeHostId });
			else if (explicitSource && explicitSource.kind !== "invalid") await refreshSource(explicitSource);
			else await refreshAll();
		} finally { setRefreshing(false); }
	};
	const notificationButton = notifications.available ? (
		<HeaderIconButton
			icon="bell"
			label="Notifications"
			badge={notifications.unreadCount}
			onPress={() => router.navigate("/notifications")}
		/>
	) : null;

	if (!configResolved) {
		return (
			<View style={styles.screen}>
				<View style={{ height: insets.top }} />
				<ScreenHeader title="Pull Requests" right={notificationButton} />
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			</View>
		);
	}

	if (invalidSource) return <View style={styles.screen}><ScreenHeader title="Pull Requests" /><EmptyState icon="git-pull-request" title="Pull request source unavailable" message="Open Pull Requests from the workspace that owns them." /></View>;
	if (!cloudAvailable && localPRs.length === 0 && (environment === "cloud" || explicitSource?.kind === "cloud")) return <View style={styles.screen}><View style={{ height: insets.top }} /><ScreenHeader title="Pull Requests" right={notificationButton} />{cloudBoard?.loading ? <View style={styles.center}><ActivityIndicator color={t.accent} /></View> : <EmptyState icon="cloud" title={explicitSource?.kind === "cloud" ? "Cloud workspace changed" : "Connect to Orchestrator Cloud"} message={cloudBoard?.error || "Open Workers to sign in or choose the workspace that owns these pull requests."} action={<Button title="Open Workers" onPress={() => router.navigate("/")} />} />}</View>;
	if (!localConfigured && !cloudAvailable && environment !== "cloud") {
		return (
			<View style={styles.screen}>
				<View style={{ height: insets.top }} />
				{/* Workers and Projects both keep their header in the unpaired state; this
				    screen dropped it, so the tab lost its title and connection lamp exactly
				    when a user most needs to know what they are looking at. */}
				<ScreenHeader title="Pull Requests" right={notificationButton} />
				<UnpairedState resolving={!configResolved} />
			</View>
		);
	}

	const counts = {
		open: prs.filter((p) => inBucket("open", prLifecycle(p.pr))).length,
		merged: prs.filter((p) => prLifecycle(p.pr) === "merged").length,
		all: prs.length,
	};

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader
				title="Pull Requests"
				right={notificationButton}
			/>
			{singleHost ? <ProjectSwitcher hostId={singleHost.hostId} selected={projectFilter}
				onSelect={(projectId) => setProjectSelection({ hostId: singleHost.hostId, projectId })} /> : null}
			{visibleHosts.map((host) => <StaleBanner
				key={host.hostId}
				sourceKey={sourceKey({ kind: "local", id: host.hostId })}
				sourceLabel="Local"
				sourceStatus={{ resolved: !host.loading, available: !!host.config, loading: host.loading, stale: !!host.error, error: host.error }}
				onRetry={onRefresh}
			/>)}
			{error && prs.length > 0 ? <View><Text accessibilityRole="alert" style={styles.sourceCopy}>{error}</Text><Button title="Retry" variant="ghost" onPress={onRefresh} /></View> : null}

			{loading && prs.length === 0 ? (
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			) : (
				<SectionList
					ref={scrollRef}
					sections={sections}
					keyExtractor={({ pr, session }) => { const cloud = cloudRows.find((row) => row.session === session && row.pr.url === pr.url); return JSON.stringify([cloud?.source.scopeKey ?? session.hostId, pr.url || `${session.projectId}#${pr.number}`]); }}
					contentContainerStyle={{ paddingBottom: 110 }}
					stickySectionHeadersEnabled={false}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={t.accent} />}
					renderSectionHeader={({ section }) => <ListSectionHeader label={section.label} />}
					renderItem={({ item: { pr, session } }) => {
						const cloud = cloudRows.find((row) => row.session === session && row.pr.url === pr.url);
						const config = cloud ? null : configForHost(session.hostId);
						return <PRCard
							pr={pr}
							session={session}
							source={cloud?.ref ?? localSources.get(session.hostId)}
							scopeKey={cloud?.source.scopeKey}
							hostId={session.hostId}
							hostName={hostStates.length > 1 ? session.hostName : undefined}
							summary={cloud?.summary ?? (config ? summaries.summaryFor(config, session.id, pr.number, pr.url) : undefined)}
						/>;
					}}
					ListEmptyComponent={
						filtered.length === 0 ? (
							error ? (
								<EmptyState
									icon={failure.icon}
									title={failure.title}
									message={failure.hint}
									action={<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={onRefresh} />}
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

			<View style={[styles.dock, { bottom: Math.max(insets.bottom, 12) }]}>
				<PRFilterDock filter={filter} counts={counts} onChange={setFilter} />
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center" },
		sourceCopy: { color: t.textTertiary, paddingHorizontal: space.lg, paddingBottom: space.sm },
		dock: {
			position: "absolute",
			left: 16,
			right: 16,
			height: 52,
			flexDirection: "row",
			alignItems: "center",
			justifyContent: "center",
			gap: space.sm,
		},
	});
