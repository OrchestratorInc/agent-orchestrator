import { useRouter } from "expo-router";
import { useMemo, useState } from "react";
import { ActivityIndicator, RefreshControl, SectionList, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useCloudAuth } from "../../lib/cloud/authStore";
import { resourceKey, sourceSlice, type Scoped } from "../../lib/environment/scopedBoard";
import { haptics } from "../../lib/haptics";
import { orchestratorProjectSections, type OrchestratorProjectRow } from "../../lib/orchestratorView";
import { ProjectCard } from "../../lib/project-card";
import { projectRoute } from "../../lib/projects-view";
import { StaleBanner } from "../../lib/StaleBanner";
import { useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { useOrchestratorLauncher } from "../../lib/useOrchestratorLauncher";
import { useTabScrollToTop } from "../../lib/useTabScrollToTop";
import { Button, EmptyState, HeaderIconButton, ListSectionHeader, ScreenHeader } from "../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function ProjectsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const router = useRouter();
	const { scopedBoard, refreshAll, notificationsUnread, hostStates } = useApp();
	const { projects, sources } = scopedBoard;
	const sourceStatuses = Object.entries(sources);
	const unreadCount = hostStates.reduce((count, host) => count + host.notificationsUnread, 0) || notificationsUnread;
	const { signedIn } = useCloudAuth();
	const [refreshing, setRefreshing] = useState(false);
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	const listRef = useTabScrollToTop<SectionList<Scoped<OrchestratorProjectRow>>>();
	const sections = useMemo(
		() => {
			const sourceEntries = new Map(projects.map((entry) => [JSON.stringify([entry.source.kind, entry.source.id]), entry.source]));
			return [...sourceEntries.values()].flatMap((source) => {
				const slice = sourceSlice(scopedBoard, source);
				return orchestratorProjectSections(slice.projects, slice.sessions, slice.orchestrators).map((section) => ({
					...section,
					title: `${source.kind === "cloud" ? "Cloud" : hostStates.find((host) => host.hostId === source.id)?.name ?? "Local"} · ${section.title}`,
					data: section.data.map((value) => ({ source, value })),
				}));
			});
		},
		[scopedBoard, projects, hostStates],
	);

	const onRefresh = async () => {
		haptics.tap();
		setRefreshing(true);
		try {
			await refreshAll();
		} finally {
			setRefreshing(false);
		}
	};

	const openProject = (entry: Scoped<OrchestratorProjectRow>) => {
		haptics.select();
		router.push(projectRoute({ source: entry.source, value: entry.value.project }));
	};
	const initialLoading = sourceStatuses.some(([, status]) => !status.resolved || status.loading);
	const anyAvailable = sourceStatuses.some(([, status]) => status.available);

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader
				title="Projects"
				right={<View style={styles.headerActions}>
					{hostStates.some((host) => host.connection === "open") && <HeaderIconButton
						icon="bell"
						label="Notifications"
						badge={unreadCount}
						onPress={() => router.navigate("/notifications")}
					/>}
					{signedIn && <HeaderIconButton icon="plus" label="Add Cloud project" onPress={() => router.push("/create-project")} />}
				</View>}
			/>
			{sourceStatuses.map(([key, status]) => (
				<StaleBanner key={key} sourceLabel={key.startsWith('["cloud"') ? "Cloud" : "Local"} sourceStatus={status} onRetry={onRefresh} />
			))}
			{initialLoading && projects.length === 0 ? (
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			) : (
				<SectionList
					ref={listRef}
					sections={sections}
					keyExtractor={(entry) => resourceKey(entry.source, entry.value.project.id)}
					contentInsetAdjustmentBehavior="automatic"
					contentContainerStyle={{ paddingBottom: insets.bottom + 92 }}
					stickySectionHeadersEnabled={false}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={t.accent} />}
					renderSectionHeader={({ section }) => (
						<ListSectionHeader label={section.title} count={section.data.length} />
					)}
					renderItem={({ item }) => (
						<ProjectCard
							row={item.value}
							sourceLabel={item.source.kind === "cloud" ? "Cloud" : "Local"}
							busy={busyProjects.has(resourceKey(item.source, item.value.project.id))}
							onOpenProject={() => openProject(item)}
							onOrchestrator={() => openOrchestrator(item)}
						/>
					)}
					ListEmptyComponent={
						!anyAvailable ? (
							<EmptyState
								icon="folder"
								title="Connect a workspace"
								message="Pair a desktop or sign in to Cloud to see projects here."
								action={<Button title="Sign in to Cloud" onPress={() => router.push("/sheets/cloud-signin")} />}
							/>
						) : (
							<EmptyState
								icon="folder"
								title="No projects"
								message="Add a project on desktop or import a GitHub repository to Cloud."
								action={signedIn ? <Button title="Add Cloud project" icon="plus" onPress={() => router.push("/create-project")} /> : undefined}
							/>
						)
					}
				/>
			)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		headerActions: { flexDirection: "row", alignItems: "center", gap: 4 },
	});
