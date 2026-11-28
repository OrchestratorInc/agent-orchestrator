import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Platform, RefreshControl, SectionList, StyleSheet, View, type ViewToken } from "react-native";
import { useFocusEffect, useLocalSearchParams } from "expo-router";
import type { Theme } from "../../lib/theme";
import { groupedRowPosition } from "../../lib/groupedRow";
import { PRCard, PRSectionHeader } from "../../lib/PRCard";
import { MenuRowWidthScope } from "../../lib/useMenuRowWidth";
import { PRFilterControl } from "../../lib/pr-filter-control";
import { ProjectSwitcher } from "../../lib/ProjectSwitcher";
import { collectPRs, prFilterCounts, prInFilter, prLifecycle, prListSections, type PRListFilter } from "../../lib/prView";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { Spinner } from "../../lib/Spinner";
import { StackRootBack } from "../../lib/StackRootBack";
import { FreshnessLine } from "../../lib/FreshnessLine";
import { useApp } from "../../lib/store";
import { useCloud } from "../../lib/cloud/CloudProvider";
import { isCloudHost } from "../../lib/cloud/mapping";
import { useCloudPRSummaries } from "../../lib/cloud/useCloudPRSummaries";
import { UnpairedState } from "../../lib/UnpairedState";
import { PR_SUMMARY_REUSE_MS, usePRSummaries } from "../../lib/usePRSummaries";
import { useRefreshActions } from "../../lib/useRefreshActions";
import { MINUTE_MS, useNow } from "../../lib/useNow";
import { useBoardFailure, useBoardFailureKind } from "../../lib/useBoardFailure";
import { useErrorShown } from "../../lib/telemetry/useErrorShown";
import { TabPageScroll } from "../../lib/TabPageScroll";
import { ToastHost } from "../../lib/Toast";
import { useToast } from "../../lib/useToast";
import { Button, EmptyState } from "../../lib/ui";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { space } from "../../lib/tokens";
import { useGlassBarInset } from "../../lib/useHeaderOverlap";
import type { DashboardPR } from "../../lib/api";
import { hostedRowKey, type HostedSession } from "../../lib/hostedRows";
import { useArchivedPRs, withArchivedPRSessions, type ArchivedPRSource } from "../../lib/archivedPRs";
import type { ArchivedPRState } from "../../lib/api";
import { useLastVerifiedConfigs } from "../../lib/useLastVerifiedConfig";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

type Filter = PRListFilter;
type PRRow = { pr: DashboardPR; session: HostedSession };

function prRowKey({ pr, session }: PRRow): string {
	return `${session.hostId}:${session.projectId}#${pr.number}`;
}

// Once the list reports viewable items, React Native also runs the key
// extractor on section headers, handing it the section rather than a row.
function listKey(item: PRRow | { key: string }): string {
	return "pr" in item && "session" in item ? prRowKey(item) : `section:${item.key}`;
}

type PRsParams = { hostId?: string; projectId?: string };

// Rows past the last one seen whose details load ahead of the scroll. Only
// archived rows wait for it: the merged history can run to hundreds of PRs, one
// request each.
const SUMMARY_LOOKAHEAD = 30;

// The ended sessions' PRs each filter shows.
const ARCHIVED_STATES: Record<PRListFilter, readonly ArchivedPRState[]> = { open: [], merged: ["merged"], all: ["merged", "closed"] };

// The ended sessions' PRs read as the page opens, whichever filter is shown.
// The daemon counts archived sessions, not PRs (a session can own several), so
// only the read lists give the filter counts the list's own PR identity.
const READ_ARCHIVED_STATES: readonly ArchivedPRState[] = ["merged", "closed"];

// Its rows share one width (see `MenuRowWidthScope`): a filter mounts a new
// set of rows, and the ones past the list's first render batch are sized on
// their first frame.
export default function PRsScreen() {
	return <MenuRowWidthScope><PRList /></MenuRowWidthScope>;
}

function PRList() {
	const t = useTheme();
	// Android's clear pill bar: the list starts below it (iOS insets itself).
	const glassInset = useGlassBarInset();
	const barInset = Platform.OS === "android" ? glassInset : 0;
	const styles = useThemedStyles(makeStyles);
	const { hostId: routeHostId, projectId: routeProjectId } = useLocalSearchParams<PRsParams>();
	const { hostStates, allSessions, configForHost, refreshAll, activeProjectId, cloud } = useApp();
	const { sessionsLoaded: cloudSessionsLoaded } = useCloud();
	const visibleHosts = useMemo(() => routeHostId ? hostStates.filter((host) => host.hostId === routeHostId) : hostStates, [hostStates, routeHostId]);
	// Orchestrator Cloud is one more environment beside the paired machines: a
	// lone machine's project switcher would hide its pull requests.
	const environmentCount = hostStates.length + (cloud ? 1 : 0);
	const singleMachine = hostStates.length === 1 && !cloud;
	const cloudShown = Boolean(cloud) && (!routeHostId || isCloudHost(routeHostId));
	const projectFilter = routeHostId ? (routeProjectId ?? "all") : singleMachine ? activeProjectId : "all";
	const [filter, setFilter] = useState<Filter>("open");
	const [refreshToken, setRefreshToken] = useState(0);
	// Each machine that pages its archive is read for the PRs of sessions that
	// have ended, merged and closed, as the page opens: Merged and All show
	// their real counts before either is opened, and their rows at once when
	// it is. A machine with an empty archive has none to read.
	const archivedSources = useMemo<ArchivedPRSource[]>(() => visibleHosts.flatMap((host) => {
		if (isCloudHost(host.hostId) || !host.archive) return [];
		const projectId = projectFilter === "all" ? undefined : projectFilter;
		const archived = projectId === undefined ? host.archive.total : host.archive.byProject[projectId] ?? 0;
		return archived > 0 ? [{ hostId: host.hostId, hostName: host.name, appearance: host.appearance, projectId, archived }] : [];
	}), [visibleHosts, projectFilter]);
	const archivedStates = ARCHIVED_STATES[filter];
	const archived = useArchivedPRs(archivedSources, { states: READ_ARCHIVED_STATES, refreshToken });
	const archivedLoading = archivedStates.some((state) => archived.loading[state]);
	const archivedKeys = useMemo(() => new Set(archived.sessions.map((session) => hostedRowKey(session.hostId, session.id))), [archived.sessions]);
	const prs = useMemo(() => {
		// Hosted rows are structurally shared across sync/notification updates.
		// Keep those identities, and deduplicate PRs within each machine only.
		// Live rows come first, so a PR keeps its live session's row.
		const byHost = new Map<string, HostedSession[]>();
		for (const session of withArchivedPRSessions(allSessions, archived.sessions)) {
			if (routeHostId && session.hostId !== routeHostId) continue;
			const rows = byHost.get(session.hostId);
			if (rows) rows.push(session);
			else byHost.set(session.hostId, [session]);
		}
		return [...byHost.values()].flatMap(sessions => collectPRs(sessions).filter(({ session }) => projectFilter === "all" || session.projectId === projectFilter));
	}, [allSessions, archived.sessions, routeHostId, projectFilter]);
	const configured = hostStates.length > 0 || Boolean(cloud);
	const loading = visibleHosts.some((host) => host.loading) || (cloudShown && !cloudSessionsLoaded);
	// A machine's failure fills the page only when nothing answers: with
	// Orchestrator Cloud listed, an empty filter is just empty.
	const error = !cloudShown && visibleHosts.every((host) => host.connection !== "open") ? visibleHosts.find((host) => host.error)?.error : null;
	const toast = useToast();
	const { show: showToast } = toast;
	useEffect(() => {
		if (archived.error) showToast(archived.error);
	}, [archived.error, showToast]);
	// Android 13 and later confirms every copy with its own clipboard preview.
	const linkCopied = useCallback(() => {
		if (Platform.OS === "android" && Number(Platform.Version) >= 33) return;
		showToast("Link copied.");
	}, [showToast]);

	// Grouped around the user's next action, matching the Workers board rather
	// than presenting a flat stream in daemon order.
	const filtered = useMemo(() => prs.filter(({ pr }) => prInFilter(filter, prLifecycle(pr))), [prs, filter]);
	const sections = useMemo(() => prListSections(prs, filter), [prs, filter]);
	// Archived sessions still loading each hold a PR in the state they were
	// asked for, so the counts do not climb page by page.
	const { merged: unloadedMerged, closed: unloadedClosed } = archived.unloaded;
	const counts = useMemo(() => {
		const loaded = prFilterCounts(prs);
		return { ...loaded, merged: loaded.merged + unloadedMerged, all: loaded.all + unloadedMerged + unloadedClosed };
	}, [prs, unloadedMerged, unloadedClosed]);
	// Name each row's repository only when the list holds more than one.
	const showProject = useMemo(() => new Set(prs.map(({ session }) => `${session.hostId}:${session.projectId}`)).size > 1, [prs]);
	const now = useNow(MINUTE_MS);

	// The rich per-PR detail the cards show lives on a separate endpoint, fetched
	// once per session and cached — see usePRSummaries. Pull-to-refresh is the
	// only thing that re-fetches it.
	// Archived rows load theirs as the list scrolls near them, in list order.
	const shownRows = useMemo(() => sections.flatMap((section) => section.data), [sections]);
	const [summaryReach, setSummaryReach] = useState(SUMMARY_LOOKAHEAD);
	const rowIndex = useRef(new Map<string, number>());
	rowIndex.current = new Map(shownRows.map((item, index) => [prRowKey(item), index]));
	const onViewableItemsChanged = useRef(({ viewableItems }: { viewableItems: ViewToken<PRRow>[] }) => {
		let last = -1;
		for (const token of viewableItems) {
			// A section header's token carries the section, not a row.
			const item = token?.item as Partial<PRRow> | undefined;
			const index = item?.pr && item.session ? rowIndex.current.get(prRowKey(item as PRRow)) : undefined;
			if (index !== undefined && index > last) last = index;
		}
		if (last >= 0) setSummaryReach((reach) => Math.max(reach, last + 1 + SUMMARY_LOOKAHEAD));
	}).current;
	const summaryTargets = useMemo(() => shownRows.flatMap(({ session }, index) => {
		if (index >= summaryReach && archivedKeys.has(hostedRowKey(session.hostId, session.id))) return [];
		const config = configForHost(session.hostId);
		return config ? [{ config, sessionId: session.id }] : [];
	}), [shownRows, summaryReach, archivedKeys, configForHost]);
	const summaries = usePRSummaries(summaryTargets);
	// Cards read their detail with the last verified connection of their machine,
	// so it stays through an app switch. Fetches above use only the live one.
	const summaryConfigFor = useLastVerifiedConfigs(configForHost);
	// A cloud session's detail comes from Orchestrator Cloud, not a daemon,
	// stamped with its pull requests' states: one merged or closed in Cloud
	// reads afresh rather than keeping its open card until the reuse runs out.
	const cloudSummaryTargets = useMemo(() => {
		const states = new Map<string, string[]>();
		for (const { pr, session } of prs) {
			if (!isCloudHost(session.hostId)) continue;
			states.set(session.id, [...(states.get(session.id) ?? []), `${pr.number}:${prLifecycle(pr)}`]);
		}
		return filtered.flatMap(({ session }) => isCloudHost(session.hostId) ? [{ sessionId: session.id, stamp: (states.get(session.id) ?? []).sort().join(",") }] : []);
	}, [filtered, prs]);
	const cloudSummaries = useCloudPRSummaries(cloudSummaryTargets);
	// The page loads its details as it opens. Coming back to it from a PR or a
	// worker reloads them once they are old, not on every return.
	const lastSummaryRefresh = useRef(Date.now());
	useFocusEffect(useCallback(() => {
		if (Date.now() - lastSummaryRefresh.current < PR_SUMMARY_REUSE_MS) return;
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
		cloudSummaries.reload();
		setRefreshToken((token) => token + 1);
	}, [summaries.reload, cloudSummaries.reload]));
	const failure = useBoardFailure();
	const failureKind = useBoardFailureKind();
	// The same condition as ListEmptyComponent's failure view below.
	const listLoading = (loading && prs.length === 0) || (archivedLoading && filtered.length === 0);
	useErrorShown(!listLoading && filtered.length === 0 && error ? failureKind : null);

	// No haptic of its own: each platform's refresh control gives the feedback
	// it gives everywhere, and the Retry buttons that run it too buzz once.
	// Retry never drives the pull spinner: see useRefreshActions.
	const { refreshing, onRefresh, retrying, retry } = useRefreshActions(async () => {
		lastSummaryRefresh.current = Date.now();
		summaries.reload();
		cloudSummaries.reload();
		setRefreshToken((token) => token + 1);
		await refreshAll();
	});

	if (!configured) {
		return (
			<View style={styles.screen} collapsable={false}>
				<StackRootBack />
				<TabPageScroll topInset={barInset}>
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
			{singleMachine && !routeHostId ? <ProjectSwitcher /> : null}
			<PRFilterControl filter={filter} counts={counts} onChange={setFilter} />
			{/* Speaks for one machine only when every PR here is on it. */}
			<FreshnessLine hostId={singleMachine ? hostStates[0].hostId : routeHostId ?? undefined} onRetry={retry} />
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
				keyExtractor={listKey}
				onViewableItemsChanged={onViewableItemsChanged}
				contentInsetAdjustmentBehavior="automatic"
				contentContainerStyle={{ flexGrow: 1, paddingTop: barInset, paddingBottom: space.xxl }}
				stickySectionHeadersEnabled={false}
				ListHeaderComponent={listHeader}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} progressViewOffset={barInset} {...refreshControlColors(t)} />}
				renderSectionHeader={({ section }) => <PRSectionHeader label={section.label} count={section.data.length} />}
				renderItem={({ item: { pr, session }, index, section }) => {
					const config = summaryConfigFor(session.hostId);
					return <PRCard
						pr={pr}
						session={session}
						hostId={session.hostId}
						hostName={environmentCount > 1 ? session.hostName : undefined}
						showProject={showProject}
						position={groupedRowPosition(index, section.data.length)}
						now={now}
						summary={isCloudHost(session.hostId) ? cloudSummaries.summaryFor(session.id, pr.number) : config ? summaries.summaryFor(config, session.id, pr.number) : undefined}
						onLinkCopied={linkCopied}
					/>;
				}}
				ListFooterComponent={archivedLoading && filtered.length > 0 ? (
					<View style={styles.footer}>
						<Spinner color={t.accent} accessibilityLabel="Loading past pull requests" />
					</View>
				) : null}
				ListEmptyComponent={
					(loading && prs.length === 0) || (archivedLoading && filtered.length === 0) ? (
						<View style={styles.center}>
							<Spinner color={t.accent} accessibilityLabel="Loading pull requests" />
						</View>
					) : filtered.length === 0 ? (
						error ? (
							<EmptyState
								icon={failure.icon}
								title={failure.title}
								message={failure.hint}
								action={<Button title="Retry" icon="refresh-cw" variant="secondary" loading={retrying} onPress={retry} />}
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
		footer: { alignItems: "center", paddingVertical: space.lg },
	});
