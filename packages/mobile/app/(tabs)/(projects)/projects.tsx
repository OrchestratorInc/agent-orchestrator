import { useRouter } from "expo-router";
import { useMemo, useState } from "react";
import { FlatList, RefreshControl, StyleSheet, View } from "react-native";
import { BoardHeaderItems } from "../../../lib/BoardHeaderItems";
import { hostedProjectKey, hostedProjectSections } from "../../../lib/hostedRows";
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
		notificationsUnread,
		refreshAll,
	} = useApp();
	const fleetLoading = hostStates.length ? hostStates.some((host) => host.loading) : loading;
	const fleetError = hostStates.length > 1
		? hostStates.every((host) => host.connection === "closed" && !host.loading)
		: Boolean(error);
	const unreadCount = hostStates.length > 1
		? hostStates.reduce((count, host) => count + host.notificationsUnread, 0)
		: notificationsUnread;
	const [refreshing, setRefreshing] = useState(false);
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	// One list of rows, no status headers: each row already says how its
	// orchestrator is doing. The sections' order is kept, so projects that need
	// attention still come first.
	const rows = useMemo(
		() => hostedProjectSections(hostStates).flatMap((section) => section.data),
		[hostStates],
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

	if (!configured && hostStates.length === 0) {
		return (
			<View style={styles.screen}>
				<TabPageScroll>
					<UnpairedState />
				</TabPageScroll>
				<BoardHeaderItems unread={0} notifications={false} />
			</View>
		);
	}

	// In the list, not above it: the list has to be the page's first view under
	// the translucent header.
	const staleBanner = hostStates.length <= 1 ? <StaleBanner error={!!error} onRetry={onRefresh} /> : null;

	return (
		<View style={styles.screen}>
			{fleetLoading && allProjects.length === 0 ? (
				<TabPageScroll>
					{staleBanner}
					<View style={styles.center}>
						<Spinner color={t.accent} accessibilityLabel="Loading projects" />
					</View>
				</TabPageScroll>
			) : (
				<FlatList
					data={rows}
					keyExtractor={(row) => hostedProjectKey(row.project)}
					contentInsetAdjustmentBehavior="automatic"
					contentContainerStyle={{ flexGrow: 1, paddingBottom: space.xxl }}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} {...refreshControlColors(t)} />}
					ListHeaderComponent={staleBanner}
					renderItem={({ item }) => (
						<ProjectCard
							row={item}
							busy={busyProjects.has(hostedProjectKey(item.project))}
							showMachine={hostStates.length > 1}
							onOpenProject={openProject}
							onOrchestrator={openOrchestrator}
						/>
					)}
					ListEmptyComponent={
						fleetError ? (
							<EmptyState
								icon={hostStates.length > 1 ? "unplug" : failure.icon}
								title={hostStates.length > 1 ? "No machines connected" : failure.title}
								message={hostStates.length > 1 ? undefined : failure.hint}
								action={<Button title="Retry" icon="refresh-cw" variant="secondary" onPress={onRefresh} />}
							/>
						) : (
							<EmptyState icon="folder" title="No projects" message="Add a project in Orchestrator to get started." />
						)
					}
				/>
			)}
			<BoardHeaderItems unread={unreadCount} />
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
