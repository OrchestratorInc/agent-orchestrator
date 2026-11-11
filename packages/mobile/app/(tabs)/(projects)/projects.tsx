import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useMemo, useRef, useState } from "react";
import { FlatList, Platform, RefreshControl, StyleSheet, View } from "react-native";
import { BoardHeaderItems } from "../../../lib/BoardHeaderItems";
import { useCloud } from "../../../lib/cloud/CloudProvider";
import { cloudAgentsOffered } from "../../../lib/cloud/plan";
import { useCloudPlan } from "../../../lib/cloud/planStore";
import { boardTitle } from "../../../lib/board-tabs";
import { useScrollToTopOnReselect } from "../../../lib/boardTabReselect";
import { hostedProjectKey, hostedProjectSections, type HostedProjectRow } from "../../../lib/hostedRows";
import { TitleActionButton, useInlineLargeTitle } from "../../../lib/inlineLargeTitle";
import { useTabBarFrost } from "../../../lib/frost";
import type { OrchestratorProjectRow } from "../../../lib/orchestratorView";
import { ProjectCard } from "../../../lib/project-card";
import { refreshControlColors } from "../../../lib/refreshControlColors";
import { Spinner } from "../../../lib/Spinner";
import { StaleBanner } from "../../../lib/StaleBanner";
import { useApp } from "../../../lib/store";
import { UnpairedState } from "../../../lib/UnpairedState";
import type { Theme } from "../../../lib/theme";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { useOrchestratorLauncher } from "../../../lib/useOrchestratorLauncher";
import { useBoardFailure } from "../../../lib/useBoardFailure";
import { TabPageScroll } from "../../../lib/TabPageScroll";
import { space } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

export default function ProjectsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const {
		configured,
		loading,
		error,
		allProjects,
		hostStates,
		cloud,
		notificationsUnread,
		refreshAll,
	} = useApp();
	const { status: cloudStatus, notificationsUnread: cloudUnread, watch: watchCloud, projectsLoaded: cloudProjectsLoaded, projectsError: cloudProjectsError, refreshProjects: refreshCloudProjects, client: cloudClient, org: cloudOrg } = useCloud();
	// A Free organization runs no cloud agents, so a new cloud project could never run anything.
	const cloudPlan = useCloudPlan(cloudClient, cloud ? cloudOrg?.id : undefined);
	const canCreateCloudProject = Boolean(cloud) && cloudAgentsOffered(cloudPlan, cloudOrg?.offering) === true;
	const cloudReady = Boolean(cloud) && cloudProjectsLoaded;
	const cloudPending = !cloud && (cloudStatus === "loading" || cloudStatus === "connecting");
	// Signed in to Cloud with its first list not in yet: still loading, not
	// "No projects". A first read that failed says so, with Retry: projects
	// aren't polled, so nothing else would read them again.
	const cloudLoading = Boolean(cloud) && !cloudProjectsLoaded && !cloudProjectsError;
	const cloudFailed = Boolean(cloud) && !cloudProjectsLoaded && Boolean(cloudProjectsError);
	useFocusEffect(useCallback(() => watchCloud(), [watchCloud]));
	// A cloud project starts from a GitHub repository, here or from New task.
	const newCloudProject = useCallback(() => router.push("/cloud/new-project"), [router]);
	// On iOS, New cloud project sits at the end of the page's own title line, so
	// the bar keeps the board's items as they are. Android's large title is the
	// bar's own, on the row its actions sit on, so there it is the bar's "+".
	const largeTitle = useInlineLargeTitle(boardTitle("(projects)"), {
		action: canCreateCloudProject ? <TitleActionButton icon="add" accessibilityLabel="New cloud project" onPress={newCloudProject} testID="new-cloud-project-title" /> : undefined,
	});
	const tabBar = useTabBarFrost();
	const listRef = useRef<FlatList<HostedProjectRow>>(null);
	useScrollToTopOnReselect("(projects)", listRef);
	const fleetLoading = hostStates.length ? hostStates.some((host) => host.loading) : loading;
	const fleetError = hostStates.length > 1
		? hostStates.every((host) => host.connection === "closed" && !host.loading)
		: Boolean(error);
	const unreadCount = (hostStates.length > 1
		? hostStates.reduce((count, host) => count + host.notificationsUnread, 0)
		: notificationsUnread) + (cloud ? cloudUnread : 0);
	const [refreshing, setRefreshing] = useState(false);
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	// One list of rows, no status headers: each row already says how its
	// orchestrator is doing. The sections' order is kept, so projects that need
	// attention still come first.
	// Orchestrator Cloud's projects sit beside each machine's, marked by the cloud environment.
	const rows = useMemo(
		() => hostedProjectSections(cloud ? [...hostStates, cloud] : hostStates).flatMap((section) => section.data),
		[cloud, hostStates],
	);
	const failure = useBoardFailure();

	// No haptic of its own: each platform's refresh control gives the feedback
	// it gives everywhere, and the Retry buttons that also call this buzz once.
	const onRefresh = async () => {
		setRefreshing(true);
		try {
			await refreshAll();
		} finally {
			setRefreshing(false);
		}
	};

	const openProject = (row: OrchestratorProjectRow) => {
		const hostId = "hostId" in row.project && typeof row.project.hostId === "string" ? row.project.hostId : undefined;
		if (!hostId) return;
		router.push({ pathname: "/project/[id]", params: { id: row.project.id, hostId } });
	};

	if (!configured && hostStates.length === 0 && !cloud && !cloudPending) {
		return (
			<View style={styles.screen}>
				<TabPageScroll>
					{largeTitle.header}
					<UnpairedState />
				</TabPageScroll>
				<BoardHeaderItems unread={0} notifications={false} />
				{largeTitle.edge}
				{tabBar.frost}
			</View>
		);
	}

	// In the list, not above it: the list has to be the page's first view under
	// the translucent header.
	const staleBanner = hostStates.length <= 1 ? <StaleBanner error={!!error} onRetry={onRefresh} /> : <StaleBanner networkOnly />;

	return (
		<View style={styles.screen}>
			{(fleetLoading || cloudPending || cloudLoading) && allProjects.length === 0 && !cloudReady ? (
				<TabPageScroll>
					{largeTitle.header}
					{staleBanner}
					<View style={styles.center}>
						<Spinner color={t.accent} accessibilityLabel="Loading projects" />
					</View>
				</TabPageScroll>
			) : (
				<FlatList
					ref={listRef}
					data={rows}
					keyExtractor={(row) => hostedProjectKey(row.project)}
					contentInsetAdjustmentBehavior="automatic"
					// Android's tab bar is clear and the list runs on under it.
					contentContainerStyle={{ flexGrow: 1, paddingBottom: space.xxl + tabBar.inset }}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} progressViewOffset={largeTitle.topInset} {...refreshControlColors(t)} />}
					ListHeaderComponent={<>{largeTitle.header}{staleBanner}</>}
					onScroll={largeTitle.onScroll}
					scrollEventThrottle={largeTitle.onScroll ? 16 : undefined}
					renderItem={({ item }) => (
						<ProjectCard
							row={item}
							busy={busyProjects.has(hostedProjectKey(item.project))}
							showMachine={hostStates.length + (cloud ? 1 : 0) > 1}
							onOpenProject={openProject}
							onOrchestrator={openOrchestrator}
						/>
					)}
					ListEmptyComponent={
						cloudFailed ? (
							<EmptyState
								icon="cloud"
								title="Couldn't load cloud projects"
								message={cloudProjectsError ?? undefined}
								action={<Button title="Retry" icon="refresh-cw" variant="secondary" onPress={() => void refreshCloudProjects()} />}
							/>
						) : fleetError && !cloud ? (
							<EmptyState
								icon={hostStates.length > 1 ? "unplug" : failure.icon}
								title={hostStates.length > 1 ? "No machines connected" : failure.title}
								message={hostStates.length > 1 ? undefined : failure.hint}
								action={<Button title="Retry" icon="refresh-cw" variant="secondary" onPress={onRefresh} />}
							/>
						) : (
							<EmptyState
								icon="folder"
								title="No projects"
								message={canCreateCloudProject ? "Create a cloud project from a GitHub repository, or add one in Orchestrator on your computer." : "Add a project in Orchestrator to get started."}
								action={canCreateCloudProject ? <Button title="New cloud project" icon="cloud" onPress={newCloudProject} /> : undefined}
							/>
						)
					}
				/>
			)}
			<BoardHeaderItems unread={unreadCount} onAdd={Platform.OS === "android" && canCreateCloudProject ? newCloudProject : undefined} addLabel="New cloud project" />
			{largeTitle.edge}
			{tabBar.frost}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// No background of its own: the stack's contentStyle paints the page. A
		// coloured view that is not a stacking context is mounted empty with its
		// children hoisted after it, which put it ahead of the list as the page's
		// first native view, so the large title and the tab bar found no scroll
		// view to follow and stayed expanded over the rows.
		screen: { flex: 1 },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
	});
