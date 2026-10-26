import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useLayoutEffect, useMemo, useState } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { hostRouteMatches } from "../../lib/hostRoute";
import { orchestratorProjectSections, projectDetailSessions, projectPageStats } from "../../lib/orchestratorView";
import { ProjectMachineIndicator, ProjectPageHeader } from "../../lib/project-card";
import { Spinner } from "../../lib/Spinner";
import { StaleBanner } from "../../lib/StaleBanner";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { toolbarIcon } from "../../lib/toolbarIcon";
import { useBoardTabBeneath } from "../../lib/useBoardTabBeneath";
import { useOrchestratorLauncher } from "../../lib/useOrchestratorLauncher";
import { Button, EmptyState, ListSectionHeader } from "../../lib/ui";
import { WorkerBoardList } from "../../lib/worker-board-list";
import type { ArchiveSource } from "../../lib/archive/useArchivedSessions";
import { hostedRowKey } from "../../lib/hostedRows";
import { AddFab, addFabClearance } from "../../lib/AddFab";
import { space } from "../../lib/tokens";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * One project: its orchestrator on top, and below it that project's workers
 * exactly as the Workers board shows them — same sections, same row actions,
 * archive included — so nothing here has to be relearned.
 */
export default function ProjectScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ProjectScreenContent /></HostScope> : <ProjectScreenContent />;
}

function ProjectScreenContent() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const { id, hostId: routeHostId } = useLocalSearchParams<{ id: string; hostId?: string }>();
	const { config, currentHostId, hostStates, connection, loading, error, refresh, projects, sessions, orchestrators, archive } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	const [refreshing, setRefreshing] = useState(false);

	const row = useMemo(
		() => hostMatches
			? orchestratorProjectSections(projects, sessions, orchestrators)
				.flatMap((section) => section.data)
				.find((candidate) => candidate.project.id === id)
			: undefined,
		[projects, sessions, orchestrators, id, hostMatches],
	);
	const title = row?.project.name ?? "Project";
	// The native header's title, and this page's name in the back-history menu
	// of a page opened from here.
	useLayoutEffect(() => navigation.setOptions({ title }), [navigation, title]);
	// Back always lands on Projects, the list this page is opened from, even when
	// a cold link opened it over the board's Workers tab.
	useBoardTabBeneath("(projects)");
	const projectSessions = useMemo(() => hostMatches ? projectDetailSessions(id ?? "", sessions) : [], [hostMatches, id, sessions]);
	// The archive is counted, not listed, by a machine that pages it.
	const pagedArchived = hostMatches && id ? archive?.byProject[id] ?? 0 : 0;
	const stats = useMemo(() => projectPageStats(projectSessions, row?.link, pagedArchived), [projectSessions, row?.link, pagedArchived]);
	const archiveSources = useMemo<ArchiveSource[]>(
		() => hostMatches && id && archive && currentHostId ? [{ hostId: currentHostId, projectId: id, summary: archive }] : [],
		[hostMatches, id, archive, currentHostId],
	);

	// No haptic of its own: each platform's refresh control gives the feedback
	// it gives everywhere, and the Retry banner that also calls this buzzes once.
	const onRefresh = useCallback(async () => {
		setRefreshing(true);
		try {
			await refresh();
		} finally {
			setRefreshing(false);
		}
	}, [refresh]);

	const startTask = useCallback(() => {
		if (!hostMatches) return;
		router.push({ pathname: "/spawn", params: { projectId: id, hostId: routeHostId } });
	}, [hostMatches, id, routeHostId, router]);
	const canStartTask = hostMatches && !!row;

	// Memoized on what the items show. hostStates is replaced on every poll, and
	// each new toolbar element would register the header's options again.
	const machine = hostStates.find((host) => host.hostId === currentHostId);
	const machineName = machine?.name;
	const machineSymbol = machine?.appearance.symbol;
	// iOS has no floating action button; its create action is a header "+".
	// Android floats Material's FAB below instead.
	const headerAdd = Platform.OS === "ios" && canStartTask;
	const headerItems = useMemo(
		() => machineName || headerAdd ? (
			<Stack.Toolbar placement="right">
				{/* Which machine this project is on: a label, not a control, so it
				    stays off the bar items' shared glass. */}
				{machineName ? (
					<Stack.Toolbar.View hidesSharedBackground>
						<ProjectMachineIndicator hostId={currentHostId} hosts={hostStates} />
					</Stack.Toolbar.View>
				) : null}
				{headerAdd ? <Stack.Toolbar.Button icon={toolbarIcon("add")} accessibilityLabel="Spawn worker" onPress={startTask} /> : null}
			</Stack.Toolbar>
		) : null,
		// eslint-disable-next-line react-hooks/exhaustive-deps -- the indicator reads only the machine's name and symbol from hostStates
		[currentHostId, headerAdd, machineName, machineSymbol, startTask],
	);

	return (
		<View style={styles.screen}>
			{headerItems}
			{hostMatches ? <StaleBanner error={!!error} onRetry={onRefresh} /> : null}

			{!hostMatches ? (
				loading && !config ? (
					<View style={styles.center}>
						<Spinner color={t.accent} accessibilityLabel="Loading project" />
					</View>
				) : (
					<EmptyState
						icon="server"
						title="Project belongs to another machine"
						message="Open it from that machine's project list."
						// Back down to the board already beneath this page: navigating from
						// here would stack a second board on top of it.
						action={<Button title="Open projects" icon="folder" onPress={() => router.dismissTo("/projects")} />}
					/>
				)
			) : !row ? (
				loading ? (
					<View style={styles.center}>
						<Spinner color={t.accent} accessibilityLabel="Loading project" />
					</View>
				) : connection !== "open" ? (
					<EmptyState icon="wifi-off" title="Machine offline" message="This project loads once the app reconnects." />
				) : (
					<EmptyState icon="folder" title="Project not found" message="It may have been removed from Orchestrator." />
				)
			) : (
				<WorkerBoardList
					sessions={projectSessions}
					archiveSources={archiveSources}
					showProject={false}
					contentBottomInset={insets.bottom + addFabClearance + space.xxl}
					refreshing={refreshing}
					onRefresh={onRefresh}
					ListHeaderComponent={
						<ProjectPageHeader
							row={row}
							stats={stats}
							busy={busyProjects.has(hostedRowKey(currentHostId ?? "", row.project.id))}
							onPress={openOrchestrator}
						/>
					}
					ListEmptyComponent={
						<View>
							<ListSectionHeader label="Workers" count={0} />
							<EmptyState
								icon="moon"
								title="No workers yet"
								message="Start a task to put this project to work."
								action={<Button title="Start task" icon="plus" onPress={startTask} />}
							/>
						</View>
					}
				/>
			)}
			{canStartTask ? <AddFab label="Spawn worker" onPress={startTask} bottomInset={insets.bottom} testID="project-spawn-worker" /> : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
	});
