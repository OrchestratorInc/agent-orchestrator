import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useMemo, useState } from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "../../lib/haptics";
import { boardFailure, boardPresentation, canUseOrchestratorAction, projectDetailState } from "../../lib/board-presentation";
import { orchestratorProjectSections, projectDetailSessions, projectPageStats } from "../../lib/orchestratorView";
import { ProjectPageHeader } from "../../lib/project-card";
import { StaleBanner } from "../../lib/StaleBanner";
import { useApp } from "../../lib/store";
import { CloudUnreadyState } from "../../lib/UnpairedState";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { useOrchestratorLauncher } from "../../lib/useOrchestratorLauncher";
import { Button, EmptyState, HeaderIconButton, ListSectionHeader, ScreenHeader } from "../../lib/ui";
import { WorkerBoardList } from "../../lib/worker-board-list";
import { WorkerDock } from "../../lib/worker-dock";
import { workerListBottomInset } from "../../lib/worker-dock-layout";
import { backOr } from "../../lib/backNavigation";
import { resourceKey, sourceSlice } from "../../lib/environment/scopedBoard";
import { resolveSessionRouteSource } from "../../lib/session/sessionRoute";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * One project: its orchestrator on top, and below it that project's workers
 * exactly as the Workers board shows them — same sections, same row actions,
 * archive included — so nothing here has to be relearned.
 */
export default function ProjectScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { id, source: sourceParam, sourceId } = useLocalSearchParams<{ id: string; source?: string; sourceId?: string }>();
	const { scopedBoard, sourceFor, refreshSource } = useApp();
	const route = resolveSessionRouteSource({ id: id ?? "", source: sourceParam, sourceId }, scopedBoard.projects);
	const source = route.kind === "found" ? route.source : null;
	const status = source ? scopedBoard.sources[source.kind] : undefined;
	const configured = source ? !!sourceFor(source) : false;
	const environment = source?.kind ?? null;
	const presentation = boardPresentation(environment, configured);
	const loading = !!status?.loading;
	const error = status?.error ?? null;
	const { projects, sessions, orchestrators } = source ? sourceSlice(scopedBoard, source) : { projects: [], sessions: [], orchestrators: [] };
	const cloudFailure = boardFailure("cloud", undefined, { host: "", port: "", platform: "" });
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	const [refreshing, setRefreshing] = useState(false);

	const row = useMemo(
		() =>
			orchestratorProjectSections(projects, sessions, orchestrators)
				.flatMap((section) => section.data)
				.find((candidate) => candidate.project.id === id),
		[projects, sessions, orchestrators, id],
	);
	const projectSessions = useMemo(() => projectDetailSessions(id ?? "", sessions), [id, sessions]);
	const stats = useMemo(() => projectPageStats(projectSessions, row?.link), [projectSessions, row?.link]);
	const detailState = projectDetailState(presentation.state, !!row, loading, environment === "cloud" && !!error);

	const onRefresh = useCallback(async () => {
		haptics.tap();
		setRefreshing(true);
		try {
			if (source) await refreshSource(source);
		} finally {
			setRefreshing(false);
		}
	}, [refreshSource, source?.kind, source?.id]);

	const startTask = () => {
		haptics.tap();
		if (source) router.push({ pathname: "/spawn", params: { projectId: id, source: source.kind, sourceId: source.id } });
	};

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader
				title={row?.project.name ?? "Project"}
				left={
					<HeaderIconButton
						icon="back"
						label="Back"
						// A deep link can open this page as the only screen in the stack, with
						// nothing beneath it to go back to. Land on Projects instead.
						onPress={() => backOr(router, "/projects")}
					/>
				}
				right={null}
			/>
			{presentation.state === "board" && <StaleBanner error={!!error} onRetry={onRefresh} />}

			{route.kind === "ambiguous" || route.kind === "invalid" || route.kind === "missing" ? (
				<EmptyState icon="folder" title="Choose a project from Projects" message="This link cannot safely identify its source."
					action={<Button title="Open Projects" onPress={() => router.navigate("/projects")} />} />
			) : source && !configured && status?.resolved ? (
				<EmptyState icon="wifi-off" title={source.kind === "cloud" ? "Cloud project unavailable" : "Desktop project unavailable"}
					message="This project belongs to a source that is no longer connected."
					action={<Button title={source.kind === "cloud" ? "Sign in to Cloud" : "Pair desktop"}
						onPress={() => router.push(source.kind === "cloud" ? "/sheets/cloud-signin" : "/pair")} />} />
			) : detailState === "cloud-unready" ? (
				<CloudUnreadyState />
			) : detailState !== "project" || !row ? (
				detailState === "loading" ? (
					<View style={styles.center}>
						<ActivityIndicator color={t.accent} />
					</View>
				) : detailState === "cloud-error" ? (
					<EmptyState icon="wifi-off" title={cloudFailure.title} message={cloudFailure.message}
						action={<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={onRefresh} />} />
				) : (
					<EmptyState icon="folder" title="Project not found" message="It may have been removed from AO." />
				)
			) : (
				<WorkerBoardList
					interactionMode={presentation.interactionMode}
					sessions={projectSessions}
					showProject={false}
					contentBottomInset={presentation.spawnControls ? workerListBottomInset(insets.bottom + 12) : insets.bottom + 32}
					refreshing={refreshing}
					onRefresh={onRefresh}
					ListHeaderComponent={
						<ProjectPageHeader
								row={row}
								stats={stats}
								busy={source ? busyProjects.has(resourceKey(source, row.project.id)) : false}
								onPress={source && canUseOrchestratorAction(environment, row.action) ? () => openOrchestrator({ source, value: row }) : undefined}
						/>
					}
					ListEmptyComponent={
						<View>
							<ListSectionHeader label="Workers" count={0} />
							<EmptyState
								icon="moon"
								title="No workers yet"
								message="Start a task to put this project to work."
								action={presentation.spawnControls ? <Button title="Start task" icon="plus" onPress={startTask} /> : null}
							/>
						</View>
					}
				/>
			)}
			{detailState === "project" && presentation.spawnControls ? (
				<View style={[styles.dock, { bottom: insets.bottom + 12 }]}>
					<WorkerDock
						controlsEnabled={false}
						query=""
						onQueryChange={() => {}}
						searchOpen={false}
						onSearchOpen={() => {}}
						onSearchClose={() => {}}
						onOpenControls={() => {}}
						projectFiltered={false}
						projects={projects}
						selectedProjectId={id ?? ""}
						onSelectProject={() => {}}
						onSpawn={startTask}
					/>
				</View>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		dock: { position: "absolute", left: 16, right: 16, height: 52, flexDirection: "row" },
	});
