import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, FlatList, Keyboard, StyleSheet, View } from "react-native";
import { AddFab, addFabClearance } from "../../../lib/AddFab";
import { StaleBanner } from "../../../lib/StaleBanner";
import { useApp } from "../../../lib/store";
import { BoardHeaderItems } from "../../../lib/BoardHeaderItems";
import { useBoardFailure } from "../../../lib/useBoardFailure";
import { UnpairedState } from "../../../lib/UnpairedState";
import type { Theme } from "../../../lib/theme";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { useTabBarInset } from "../../../lib/useTabBarInset";
import { useHeaderOverlap } from "../../../lib/useHeaderOverlap";
import { setNewTaskHandler } from "../../../lib/newTaskAction";
import { Button, EmptyState } from "../../../lib/ui";
import { WorkerBoardList, type BoardRow } from "../../../lib/worker-board-list";
import type { ArchiveSource } from "../../../lib/archive/useArchivedSessions";
import { hostedProjectKey } from "../../../lib/hostedRows";
import { workerFilterMenu } from "../../../lib/worker-filter-menu";
import { WorkerMachineState } from "../../../lib/worker-machine-state";
import { WorkerEnvironmentFilter } from "../../../lib/worker-environment-filter";
import {
	ALL_WORKER_ENVIRONMENTS,
	ALL_WORKER_PROJECTS,
	filterWorkersByEnvironmentAndProject,
	showsEnvironmentFilter,
	sortWorkerEnvironments,
	spawnProjectParam,
	workerProjectForEnvironment,
	workerProjectLabel,
	workerProjectOptions,
} from "../../../lib/worker-controls";
import { space } from "../../../lib/tokens";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

export default function FleetScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);

	const router = useRouter();
	const tabBarInset = useTabBarInset();
	const headerOverlap = useHeaderOverlap();
	const { config, configured, configResolved, connection, loading, error, refreshAll, refreshHost, allSessions, allProjects, hostStates, notificationsUnread } = useApp();
	const fleetLoading = hostStates.length ? hostStates.some((host) => host.loading) : loading;
	const fleetError = hostStates.length > 1
		? hostStates.every((host) => host.connection === "closed" && !host.loading)
		: Boolean(error);
	const unreadCount = hostStates.length > 1
		? hostStates.reduce((count, host) => count + host.notificationsUnread, 0)
		: notificationsUnread;
	const [refreshing, setRefreshing] = useState(false);
	const [workerEnvironmentId, setWorkerEnvironmentId] = useState(ALL_WORKER_ENVIRONMENTS);
	const [workerProjectId, setWorkerProjectId] = useState(ALL_WORKER_PROJECTS);
	// hostStates is replaced on every poll (each machine stamps lastSyncAt), but
	// the memoised header menu only needs who the machines are. Rebuilding this on
	// every tick would register the header again mid-tap, the bug the stable
	// handlers below avoid.
	const environmentsKey = JSON.stringify(hostStates.map((host) => [host.hostId, host.name, host.appearance.symbol]));
	const environments = useMemo(
		() => sortWorkerEnvironments(hostStates.map((host) => ({ id: host.hostId, name: host.name, appearance: host.appearance }))),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- environmentsKey covers every field read here
		[environmentsKey],
	);
	const projectOptions = useMemo(
		() => workerProjectOptions(allProjects, workerEnvironmentId),
		[allProjects, workerEnvironmentId],
	);
	// The machine the app reaches first, as a value: hostStates itself is new on
	// every poll, and the spawn action is a header item.
	const openHostId = hostStates.find((host) => host.connection === "open")?.hostId;
	const spawnWorker = useCallback(() => {
		Keyboard.dismiss();
		const project = spawnProjectParam(workerProjectId, allProjects);
		const connectedHostId = connection === "open" ? config?.hostId : openHostId;
		const selectedHostId = workerEnvironmentId === ALL_WORKER_ENVIRONMENTS ? undefined : workerEnvironmentId;
		router.push({ pathname: "/spawn", params: { hostId: project?.hostId ?? selectedHostId ?? connectedHostId ?? config?.hostId, ...project } });
	}, [allProjects, config?.hostId, connection, openHostId, router, workerEnvironmentId, workerProjectId]);
	// The tab bar's New task button starts a task on the board's own machine
	// and project filters while the board is the page in front.
	useFocusEffect(useCallback(() => setNewTaskHandler(spawnWorker), [spawnWorker]));
	const listRef = useRef<FlatList<BoardRow>>(null);

	const projectSessions = useMemo(
		() => filterWorkersByEnvironmentAndProject(allSessions, workerEnvironmentId, workerProjectId, allProjects),
		[allSessions, allProjects, workerEnvironmentId, workerProjectId],
	);
	// Each machine that pages its archive, narrowed by the same machine and
	// project filters as the live list. Keyed on the facts it reads, because
	// hostStates is a new array on every poll.
	const archiveSourcesKey = JSON.stringify(hostStates.map((host) => [host.hostId, host.name, host.appearance.symbol, host.archive ?? null]))
		+ `|${workerEnvironmentId}|${workerProjectId}|${JSON.stringify(allProjects.map(hostedProjectKey))}`;
	const archiveSources = useMemo<ArchiveSource[]>(() => {
		const project = workerProjectId === ALL_WORKER_PROJECTS ? undefined : allProjects.find((item) => hostedProjectKey(item) === workerProjectId);
		return hostStates.flatMap((host): ArchiveSource[] => {
			if (!host.archive) return [];
			if (workerEnvironmentId !== ALL_WORKER_ENVIRONMENTS && host.hostId !== workerEnvironmentId) return [];
			if (workerProjectId !== ALL_WORKER_PROJECTS && project?.hostId !== host.hostId) return [];
			return [{ hostId: host.hostId, projectId: project?.id, host: { name: host.name, appearance: host.appearance }, summary: host.archive }];
		});
		// eslint-disable-next-line react-hooks/exhaustive-deps -- archiveSourcesKey covers every field read here
	}, [archiveSourcesKey]);
	const boardIdentityKey = `${workerEnvironmentId}|${workerProjectId}`;
	const selectedProjectLabel = workerProjectLabel(allProjects, workerProjectId, workerEnvironmentId);
	const selectedEnvironmentLabel = environments.find((environment) => environment.id === workerEnvironmentId)?.name;
	const selectedHost = hostStates.find((host) => host.hostId === workerEnvironmentId);
	const selectEnvironment = useCallback((environmentId: string) => {
		// Resolve both selectors in one React event. Letting the project reset in the
		// effect below creates an intermediate empty board and a second remount.
		setWorkerEnvironmentId(environmentId);
		setWorkerProjectId((projectId) => workerProjectForEnvironment(allProjects, environmentId, projectId));
	}, [allProjects]);
	const filterMenu = useMemo(() => workerFilterMenu({
		environments,
		selectedEnvironmentId: workerEnvironmentId,
		onSelectEnvironment: selectEnvironment,
		projects: allProjects,
		selectedProjectId: workerProjectId,
		onSelectProject: setWorkerProjectId,
		badgeColor: t.accent,
	}), [allProjects, environments, selectEnvironment, t.accent, workerEnvironmentId, workerProjectId]);

	useEffect(() => {
		if (
			workerEnvironmentId !== ALL_WORKER_ENVIRONMENTS &&
			!environments.some((environment) => environment.id === workerEnvironmentId)
		) {
			setWorkerEnvironmentId(ALL_WORKER_ENVIRONMENTS);
		}
	}, [environments, workerEnvironmentId]);

	useEffect(() => {
		if (
			workerProjectId !== ALL_WORKER_PROJECTS &&
			!projectOptions.some((project) => project.id === workerProjectId)
		) {
			setWorkerProjectId(ALL_WORKER_PROJECTS);
		}
	}, [projectOptions, workerProjectId]);

	// The poll's failure as the same human copy the pairing screens use.
	const failure = useBoardFailure();

	// No haptic of its own: each platform's refresh control gives the feedback
	// it gives everywhere, and the Retry actions that also call this buzz once.
	const onRefresh = useCallback(async () => {
		setRefreshing(true);
		try {
			if (selectedHost) await refreshHost(selectedHost.hostId);
			else await refreshAll();
		}
		finally { setRefreshing(false); }
	}, [refreshAll, refreshHost, selectedHost?.hostId]);

	// Unpaired only once the stored config has been read: until then an empty
	// store means "still finding the machine", and the board shows its spinner.
	const unpaired = configResolved && !configured && hostStates.length === 0;
	const resolving = !configResolved && !configured && hostStates.length === 0;

	// Inside the list rather than above it: the header is translucent and the page
	// is laid out under it, so the list has to be the page's first view. Above the
	// rows rather than inside ListEmptyComponent, because the case the banner
	// exists for is a populated board whose poll has died.
	const listHeader = unpaired ? null : (
		<>
			{hostStates.length <= 1 ? <StaleBanner error={!!error} onRetry={onRefresh} /> : null}
			{showsEnvironmentFilter(environments) ? (
				<WorkerEnvironmentFilter
					environments={environments}
					selectedEnvironmentId={workerEnvironmentId}
					onSelectEnvironment={selectEnvironment}
				/>
			) : null}
		</>
	);

	return (
		<View style={styles.screen}>
			{/* The board is the page's one scroll view in every state, unpaired and
			    loading included: the bar and the tab bar bind to the first scroll
			    view the page shows, and a placeholder swapped out for the list left
			    them tracking a view that was gone. */}
			<WorkerBoardList
				key={boardIdentityKey}
				sessions={projectSessions}
				archiveSources={archiveSources}
				identityKey={boardIdentityKey}
				listRef={listRef}
				// iOS insets the list by the tab bar already; Android pads it clear
				// of the floating New button too.
				contentBottomInset={addFabClearance + space.xxl}
				contentInsetAdjustmentBehavior="automatic"
				overlayTopInset={headerOverlap}
				refreshing={refreshing}
				onRefresh={onRefresh}
				ListHeaderComponent={listHeader}
				ListEmptyComponent={
					unpaired ? (
						<UnpairedState />
					) : resolving || (!selectedHost && fleetLoading && allSessions.length === 0) ? (
						<View style={styles.center}>
							<ActivityIndicator color={t.accent} />
						</View>
					) : selectedHost && selectedHost.connection !== "open" ? (
						<WorkerMachineState
							host={selectedHost}
							environments={environments}
							onSelectEnvironment={selectEnvironment}
							onRetry={onRefresh}
						/>
					) : fleetError ? (
						<EmptyState
							icon={hostStates.length > 1 ? "unplug" : failure.icon}
							title={hostStates.length > 1 ? "No machines connected" : failure.title}
							message={hostStates.length > 1 ? undefined : failure.hint}
							action={
								<View style={styles.errorActions}>
									<Button title="Retry" icon="refresh-cw" variant="secondary" onPress={onRefresh} />
									{/* Re-scanning is the only fix for a rotated password, and the
									    fastest one for a moved/renamed host — so it belongs beside
									    Retry rather than three taps away in Settings. */}
									<Button title="Scan" icon="qrScanner" variant="primary" onPress={() => router.push("/pair")} />
								</View>
							}
						/>
					) : workerProjectId !== ALL_WORKER_PROJECTS ? (
						<EmptyState
							icon="folder"
							title={`No workers in ${selectedProjectLabel}`}
							message="Choose another project from the filter menu."
						/>
					) : workerEnvironmentId !== ALL_WORKER_ENVIRONMENTS ? (
						<EmptyState
							icon="monitor"
							title={`No workers on ${selectedEnvironmentLabel ?? "this machine"}`}
							message="Choose another machine from the filter menu."
						/>
					) : (
						<EmptyState
							icon="moon"
							title="No active workers"
							message="Spawn a worker to get started."
							action={<Button title="New agent" icon="plus" onPress={spawnWorker} />}
						/>
					)
				}
			/>

			{unpaired ? (
				// Settings is where pairing starts; there is nothing yet to filter
				// or spawn.
				<BoardHeaderItems unread={0} notifications={false} />
			) : (
				<>
					<AddFab label="Spawn worker" onPress={spawnWorker} bottomInset={tabBarInset} testID="spawn-worker" />
					{/* New task is the tab bar's own round button on iOS and the
					    floating New button on Android; the header keeps settings and
					    alerts. */}
					<BoardHeaderItems unread={unreadCount} filter={filterMenu} />
				</>
			)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// No background of its own: the stack's contentStyle paints the page. A
		// coloured view that is not a stacking context is mounted empty with its
		// children hoisted after it, which put it ahead of the list as the page's
		// first native view, so the bar and the tab bar found no scroll view to
		// follow.
		screen: { flex: 1 },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		errorActions: { flexDirection: "row", flexWrap: "wrap", justifyContent: "center", gap: space.sm, alignItems: "center" },
	});
