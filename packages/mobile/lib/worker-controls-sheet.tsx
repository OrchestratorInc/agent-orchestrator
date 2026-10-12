import { Feather } from "./icons";
import BottomSheet, { BottomSheetScrollView, BottomSheetView } from "@expo/ui/community/bottom-sheet";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "./haptics";
import { MachineAvatar } from "./MachineIdentity";
import { machineAppearance } from "./machineAppearance";
import { useWorkerControlsDraft } from "./useWorkerControlsDraft";
import type { Theme } from "./theme";
import { useTheme } from "./ThemeProvider";
import { ALL_WORKER_ENVIRONMENTS, ALL_WORKER_PROJECTS, workerEnvironmentOptions, workerProjectOptions, type WorkerEnvironment } from "./worker-controls";
import { iconSize, press, space, type } from "./tokens";

export function WorkerControlsSheet({
	open,
	onDismiss,
	onSearch,
	environments,
	selectedEnvironmentId,
	onSelectEnvironment,
	projects,
	selectedProjectId,
	onSelectProject,
}: {
	open: boolean;
	onDismiss: () => void;
	onSearch: () => void;
	environments: readonly WorkerEnvironment[];
	selectedEnvironmentId: string;
	onSelectEnvironment: (environmentId: string) => void;
	projects: { id: string; name: string; hostId?: string; hostName?: string }[];
	selectedProjectId: string;
	onSelectProject: (projectId: string) => void;
}) {
	const t = useTheme();
	const styles = makeStyles(t);
	const insets = useSafeAreaInsets();
	const draft = useWorkerControlsDraft({ open, selectedEnvironmentId, selectedProjectId, projects, onSelectEnvironment, onSelectProject, onDismiss, onSearch });
	const environmentOptions = workerEnvironmentOptions(environments);
	const projectOptions = workerProjectOptions(projects, draft.environmentId);

	return (
		<BottomSheet
			index={open ? 0 : -1}
			snapPoints={["55%", "85%"]}
			enablePanDownToClose
			enableDynamicSizing={false}
			backgroundStyle={{ backgroundColor: t.bgSurface }}
			onClose={draft.didClose}
		>
			<BottomSheetView style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, 16) }]}>
				<View style={styles.header}>
					<View>
						<Text style={styles.title}>Workers</Text>
						<Text style={styles.subtitle}>Find and scope this list.</Text>
					</View>
					<Pressable accessibilityRole="button" accessibilityLabel="Close" onPress={draft.close} hitSlop={12}>
						<Feather name="x" size={iconSize.lg} color={t.textSecondary} />
					</Pressable>
				</View>

				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Search workers"
					testID="worker-controls-search"
					onPress={() => {
						haptics.tap();
						draft.search();
					}}
					style={({ pressed }) => [styles.searchRow, pressed && styles.pressed]}
				>
					<Feather name="search" size={iconSize.lg} color={t.accent} />
					<Text style={styles.searchLabel}>Search workers</Text>
					<Feather name="chevron-right" size={iconSize.md} color={t.textFaint} />
				</Pressable>

				<Text style={styles.sectionLabel}>ENVIRONMENTS</Text>
				<View style={styles.optionList}>
					{environmentOptions.map((environment, index) => {
						const selected = environment.id === draft.environmentId;
						return (
							<Pressable
								key={environment.id}
								accessibilityRole="button"
								accessibilityState={{ selected }}
								testID={environment.id === ALL_WORKER_ENVIRONMENTS ? "worker-environment-filter" : undefined}
								onPress={() => {
									haptics.select();
									draft.selectEnvironment(environment.id);
								}}
								style={({ pressed }) => [styles.optionRow, index > 0 && styles.separator, selected && styles.selectedRow, pressed && styles.pressed]}
							>
								{environment.id === ALL_WORKER_ENVIRONMENTS
									? <Feather name="server" size={iconSize.md} color={t.textSecondary} />
									: <MachineAvatar appearance={machineAppearance({ id: environment.id, name: environment.label, appearance: environment.appearance })} size={30} />}
								<Text numberOfLines={1} style={[styles.optionLabel, selected && styles.selectedLabel]}>{environment.label}</Text>
								{selected ? <Feather name="check" size={iconSize.lg} color={t.accent} /> : null}
							</Pressable>
						);
					})}
				</View>

				<Text style={styles.sectionLabel}>PROJECTS</Text>
				<BottomSheetScrollView style={styles.projectList} showsVerticalScrollIndicator={false}>
					{projectOptions.map((project, index) => {
							const selected = project.id === draft.projectId;
						return (
							<Pressable
								key={project.id}
								accessibilityRole="button"
								accessibilityState={{ selected }}
								testID={project.id === ALL_WORKER_PROJECTS ? "worker-project-filter" : undefined}
								onPress={() => {
									haptics.select();
									draft.selectProject(project.id);
								}}
								style={({ pressed }) => [styles.optionRow, index > 0 && styles.separator, selected && styles.selectedRow, pressed && styles.pressed]}
							>
								<Feather name={project.id === ALL_WORKER_PROJECTS ? "layers" : "folder"} size={iconSize.md} color={selected ? t.accent : t.textSecondary} />
								<Text numberOfLines={1} style={[styles.optionLabel, selected && styles.selectedLabel]}>{project.label}</Text>
								{selected ? <Feather name="check" size={iconSize.lg} color={t.accent} /> : null}
							</Pressable>
						);
					})}
				</BottomSheetScrollView>
			</BottomSheetView>
		</BottomSheet>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	sheet: { flex: 1, paddingHorizontal: space.lg, backgroundColor: t.bgSurface },
	header: { minHeight: 58, flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: space.xxs },
	title: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title2.fontSize, lineHeight: type.title2.lineHeight, fontWeight: "600" },
	subtitle: { fontFamily: "Geist_400Regular", marginTop: space.hair, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	searchRow: { height: 52, marginTop: space.sm, paddingHorizontal: space.lg, flexDirection: "row", alignItems: "center", gap: space.md, borderRadius: 16, borderCurve: "continuous", backgroundColor: t.bgElevated, overflow: "hidden" },
	searchLabel: { fontFamily: "Geist_600SemiBold", flex: 1, color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600" },
	sectionLabel: { fontFamily: "Geist_600SemiBold", marginTop: space.xl, marginBottom: space.sm, paddingHorizontal: space.xxs, color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, letterSpacing: 1, fontWeight: "600" },
	optionList: { borderRadius: 16, borderCurve: "continuous", backgroundColor: t.bgElevated, overflow: "hidden" },
	projectList: { flex: 1, borderRadius: 16, borderCurve: "continuous", backgroundColor: t.bgElevated, overflow: "hidden" },
	optionRow: { minHeight: 52, paddingHorizontal: space.lg, flexDirection: "row", alignItems: "center", gap: space.md },
	separator: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	selectedRow: { backgroundColor: t.accentTint },
	optionLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
	selectedLabel: { fontFamily: "Geist_600SemiBold", color: t.accent, fontWeight: "600" },
	pressed: { opacity: press.opacity },
});
