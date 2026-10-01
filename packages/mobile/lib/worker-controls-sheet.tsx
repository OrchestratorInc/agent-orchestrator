import { Feather } from "./icons";
import BottomSheet, { BottomSheetScrollView, BottomSheetView } from "@expo/ui/community/bottom-sheet";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ProjectInfo } from "./api";
import type { Scoped } from "./environment/scopedBoard";
import type { EnvironmentKind } from "./environment/types";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme } from "./ThemeProvider";
import { ALL_WORKER_PROJECTS, scopedWorkerProjectOptions } from "./worker-controls";
import { iconSize, press, space, type } from "./tokens";

export function WorkerControlsSheet({
	open,
	onDismiss,
	onSearch,
	projects,
	environmentFilter,
	onSelectEnvironment,
	selectedProjectId,
	onSelectProject,
}: {
	open: boolean;
	onDismiss: () => void;
	onSearch: () => void;
	projects: readonly Scoped<ProjectInfo>[];
	environmentFilter: "all" | EnvironmentKind;
	onSelectEnvironment: (environment: "all" | EnvironmentKind) => void;
	selectedProjectId: string;
	onSelectProject: (projectId: string) => void;
}) {
	const t = useTheme();
	const styles = makeStyles(t);
	const insets = useSafeAreaInsets();
	const options = scopedWorkerProjectOptions(projects, environmentFilter);

	return (
		<BottomSheet
			index={open ? 0 : -1}
			snapPoints={["55%", "85%"]}
			enablePanDownToClose
			enableDynamicSizing={false}
			backgroundStyle={{ backgroundColor: t.bgSurface }}
			onClose={onDismiss}
		>
			<BottomSheetView style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, 16) }]}>
				<View style={styles.header}>
					<View>
						<Text style={styles.title}>Workers</Text>
						<Text style={styles.subtitle}>Find and scope this list.</Text>
					</View>
					<Pressable accessibilityRole="button" accessibilityLabel="Close" onPress={onDismiss} hitSlop={12}>
						<Feather name="x" size={iconSize.lg} color={t.textSecondary} />
					</Pressable>
				</View>

				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Search workers"
					testID="worker-controls-search"
					onPress={() => {
						haptics.tap();
						onDismiss();
						setTimeout(onSearch, 280);
					}}
					style={({ pressed }) => [styles.searchRow, pressed && styles.pressed]}
				>
					<Feather name="search" size={iconSize.lg} color={t.accent} />
					<Text style={styles.searchLabel}>Search workers</Text>
					<Feather name="chevron-right" size={iconSize.md} color={t.textFaint} />
				</Pressable>

				<Text style={styles.sectionLabel}>ENVIRONMENT</Text>
				<View style={styles.environmentOptions}>
					{(["all", "local", "cloud"] as const).map((option) => (
						<Pressable key={option} accessibilityRole="button" accessibilityState={{ selected: environmentFilter === option }}
							onPress={() => { haptics.select(); onSelectEnvironment(option); }}
							style={[styles.environmentOption, environmentFilter === option && styles.selectedRow]}>
							<Text style={[styles.projectLabel, environmentFilter === option && styles.selectedLabel]}>{option === "all" ? "All" : option === "local" ? "Local" : "Cloud"}</Text>
						</Pressable>
					))}
				</View>
				<Text style={styles.sectionLabel}>PROJECTS</Text>
				<BottomSheetScrollView style={styles.projectList} showsVerticalScrollIndicator={false}>
					{options.map((project, index) => {
						const selected = project.id === selectedProjectId;
						return (
							<Pressable
								key={project.id}
								accessibilityRole="button"
								accessibilityState={{ selected }}
								testID={project.id === ALL_WORKER_PROJECTS ? "worker-project-filter" : undefined}
								onPress={() => {
									haptics.select();
									onSelectProject(project.id);
									onDismiss();
								}}
								style={({ pressed }) => [styles.projectRow, index > 0 && styles.separator, selected && styles.selectedRow, pressed && styles.pressed]}
							>
								<Feather name={project.id === ALL_WORKER_PROJECTS ? "layers" : "folder"} size={iconSize.md} color={selected ? t.accent : t.textSecondary} />
								<Text numberOfLines={1} style={[styles.projectLabel, selected && styles.selectedLabel]}>{project.label}</Text>
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
	projectList: { flex: 1, borderRadius: 16, borderCurve: "continuous", backgroundColor: t.bgElevated, overflow: "hidden" },
	environmentOptions: { flexDirection: "row", gap: space.sm },
	environmentOption: { flex: 1, minHeight: 44, alignItems: "center", justifyContent: "center", borderRadius: 12, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	projectRow: { minHeight: 52, paddingHorizontal: space.lg, flexDirection: "row", alignItems: "center", gap: space.md },
	separator: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	selectedRow: { backgroundColor: t.accentTint },
	projectLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
	selectedLabel: { fontFamily: "Geist_600SemiBold", color: t.accent, fontWeight: "600" },
	pressed: { opacity: press.opacity },
});
