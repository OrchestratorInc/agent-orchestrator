import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, FlatList, Keyboard, Platform, StyleSheet, View } from "react-native";
import { useKeyboardState } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { boardFailure, boardPresentation } from "../../lib/board-presentation";
import { tunnelMayHaveRotated } from "../../lib/staleTunnel";
import { haptics } from "../../lib/haptics";
import { StaleBanner } from "../../lib/StaleBanner";
import { useApp } from "../../lib/store";
import { CloudUnreadyState, UnpairedState } from "../../lib/UnpairedState";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { useTabScrollToTop } from "../../lib/useTabScrollToTop";
import { Button, EmptyState, HeaderIconButton, ScreenHeader } from "../../lib/ui";
import { WorkerBoardList, type BoardRow } from "../../lib/worker-board-list";
import { WorkerDock } from "../../lib/worker-dock";
import { workerDockKeyboardLayout, workerListBottomInset } from "../../lib/worker-dock-layout";
import { WorkerControlsSheet } from "../../lib/worker-controls-sheet";
import {
	ALL_WORKER_PROJECTS,
	filterWorkersByProject,
	spawnProjectParam,
	workerProjectLabel,
	workerSearchPresentation,
} from "../../lib/worker-controls";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function FleetScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { environment, configured, loading, error, errorStatus, connection, config, refresh, sessions, projects, notificationsUnread, activeEndpoints } =
		useApp();
	const presentation = boardPresentation(environment, configured);
	const [refreshing, setRefreshing] = useState(false);
	const [query, setQuery] = useState("");
	const [searchRequested, setSearchRequested] = useState(false);
	const [controlsOpen, setControlsOpen] = useState(false);
	const [workerProjectId, setWorkerProjectId] = useState(ALL_WORKER_PROJECTS);
	// Two selectors rather than the whole state object, so the board re-renders
	// only when one of these two values actually changes.
	//
	// The hook listens on keyboardWillShow / keyboardDidHide — `will`, not `did`.
	// That is the fix: the Android branch this replaces listened for
	// keyboardDidShow, which fires only once the IME has finished animating, so
	// the dock and the list inset arrived a beat after the keyboard had landed.
	const keyboardHeight = useKeyboardState((state) => state.height);
	const keyboardVisible = useKeyboardState((state) => state.isVisible);
	const listRef = useTabScrollToTop<FlatList<BoardRow>>();

	const projectSessions = useMemo(
		() => filterWorkersByProject(sessions, presentation.localControls ? workerProjectId : ALL_WORKER_PROJECTS),
		[sessions, workerProjectId, presentation.localControls],
	);
	const searchOpen = workerSearchPresentation(searchRequested, query) === "expanded";
	const selectedProjectLabel = workerProjectLabel(projects, workerProjectId);

	useEffect(() => {
		if (
			workerProjectId !== ALL_WORKER_PROJECTS &&
			!projects.some((project) => project.id === workerProjectId)
		) {
			setWorkerProjectId(ALL_WORKER_PROJECTS);
		}
	}, [projects, workerProjectId]);

	const failure = useMemo(
		() =>
			boardFailure(
				environment,
				errorStatus ?? undefined,
				{
					host: config?.host ?? "",
					port: config?.httpPort ?? "",
					platform: Platform.OS,
				},
				tunnelMayHaveRotated(activeEndpoints, connection === "open"),
			),
		[environment, errorStatus, config?.host, config?.httpPort, activeEndpoints, connection],
	);

	const onRefresh = useCallback(async () => {
		haptics.tap();
		setRefreshing(true);
		await refresh();
		setRefreshing(false);
	}, [refresh]);

	const keyboardLayout = workerDockKeyboardLayout(keyboardHeight, insets.bottom, keyboardVisible);

	// The persisted environment choice hasn't loaded yet. Rendering either
	// empty state here would guess — and for a returning cloud user, the local
	// one guesses wrong: "no desktop paired" for someone who deliberately
	// chose not to use one. Same convention as `shouldShowLoading` for
	// `config` in lib/configLoading.ts: unresolved means "still working out
	// what to show", not "assume the common case".
	if (presentation.state === "loading") {
		return (
			<View style={styles.screen}>
				<View style={{ height: insets.top }} />
				<ScreenHeader title="Workers" />
				<View style={styles.center}>
					<ActivityIndicator color={t.blue} />
				</View>
			</View>
		);
	}

	if (presentation.state === "cloud-unready") {
		return (
			<View style={styles.screen}>
				<View style={{ height: insets.top }} />
				<ScreenHeader title="Workers" />
				<CloudUnreadyState />
			</View>
		);
	}

	if (presentation.state === "unpaired") {
		return (
			<View style={styles.screen}>
				<View style={{ height: insets.top }} />
				<ScreenHeader title="Workers" />
				<UnpairedState />
			</View>
		);
	}

	return (
		<View style={[styles.screen, { paddingBottom: presentation.localControls ? keyboardLayout.rootPaddingBottom : 0 }]}>
			<View style={{ height: insets.top }} />
			<ScreenHeader
				title="Workers"
				right={presentation.localControls ?
					<HeaderIconButton
						icon="bell"
						label="Notifications"
						badge={notificationsUnread}
						onPress={() => router.navigate("/notifications")}
					/>
					: null
				}
			/>
			{/* Above the list rather than inside ListEmptyComponent: the case this
			    exists for is a populated board whose poll has died. */}
			<StaleBanner error={!!error} onRetry={onRefresh} />

			{loading && sessions.length === 0 ? (
				<View style={styles.center}>
					<ActivityIndicator color={t.blue} />
				</View>
			) : (
				<WorkerBoardList
					interactionMode={presentation.interactionMode}
					sessions={projectSessions}
					query={presentation.localControls ? query : ""}
					listRef={listRef}
					contentBottomInset={presentation.spawnControls ? workerListBottomInset(keyboardLayout.dockBottom) : insets.bottom + 32}
					refreshing={refreshing}
					onRefresh={onRefresh}
					ListEmptyComponent={
						presentation.localControls && query.trim() ? (
							<EmptyState icon="search" title="No workers found" message={`No workers match “${query.trim()}”.`} />
						) : error ? (
							<EmptyState
								icon="wifi-off"
								title={failure.title}
								message={failure.message}
								action={
									<View style={styles.errorActions}>
										<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={onRefresh} />
										{/* Re-scanning is the only fix for a rotated password, and the
										    fastest one for a moved/renamed host — so it belongs beside
										    Retry rather than three taps away in Settings. */}
										{presentation.localControls && <Button title="Scan" icon="maximize" onPress={() => router.push("/pair")} />}
									</View>
								}
							/>
						) : presentation.localControls && workerProjectId !== ALL_WORKER_PROJECTS ? (
							<EmptyState
								icon="folder"
								title={`No workers in ${selectedProjectLabel}`}
								message="Choose another project from the Workers controls."
							/>
						) : (
							<EmptyState
								icon="moon"
								title="No active workers"
								message={presentation.localControls ? "Spawn a worker to put your fleet to work." : "Start a Cloud worker for one of your projects."}
								action={presentation.spawnControls ? <Button title="New worker" icon="plus" onPress={() => router.push({ pathname: "/spawn", params: spawnProjectParam(workerProjectId) })} /> : null}
							/>
						)
					}
				/>
			)}

			{presentation.spawnControls && <View style={[styles.dock, { bottom: keyboardLayout.dockBottom }]}>
				<WorkerDock
					controlsEnabled={presentation.localControls}
					query={query}
					onQueryChange={setQuery}
					searchOpen={searchOpen}
					onSearchOpen={() => setSearchRequested(true)}
					onSearchClose={() => {
						Keyboard.dismiss();
						setQuery("");
						setSearchRequested(false);
					}}
					onOpenControls={() => {
						Keyboard.dismiss();
						haptics.tap();
						setControlsOpen(true);
					}}
					projectFiltered={workerProjectId !== ALL_WORKER_PROJECTS}
					projects={projects}
					selectedProjectId={workerProjectId}
					onSelectProject={setWorkerProjectId}
					onSpawn={() => {
						Keyboard.dismiss();
						haptics.tap();
						router.push({ pathname: "/spawn", params: spawnProjectParam(workerProjectId) });
					}}
				/>
			</View>}

			{presentation.localControls && <WorkerControlsSheet
				open={controlsOpen}
				onDismiss={() => setControlsOpen(false)}
				onSearch={() => setSearchRequested(true)}
				projects={projects}
				selectedProjectId={workerProjectId}
				onSelectProject={setWorkerProjectId}
			/>}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		errorActions: { flexDirection: "row", gap: 10, alignItems: "center" },
		dock: {
			position: "absolute",
			left: 16,
			right: 16,
			height: 52,
			flexDirection: "row",
		},
	});
