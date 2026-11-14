import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { startTransition, useRef } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import type { Theme } from "./theme";
import { haptics } from "./haptics";
import { usePressFeedback } from "./pressFeedback";
import { activeProjectLabel, ALL_PROJECTS, resolveActiveProject } from "./projectFilter";
import { useApp } from "./store";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, touchTarget, type } from "./tokens";

/**
 * Scopes the pull-request list to one project (or all of them): a row naming
 * the current scope, which opens the system's pull-down menu with a check on
 * the current choice. A SwiftUI menu on iOS, a Material dropdown on Android.
 *
 * The choice applies in place, with no navigation: the list is already on
 * screen, so the filter changing under the menu is the whole feedback.
 */
export function ProjectSwitcher() {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const menu = useRef<MenuComponentRef>(null);
	const { projects, projectsKnown, activeProjectId, setActiveProject } = useApp();

	// Nothing to switch between — a single-project user never sees this.
	if (projects.length <= 1) return null;

	const applied = resolveActiveProject(activeProjectId, projects, projectsKnown);
	const label = activeProjectLabel(activeProjectId, projects, projectsKnown);
	const scoped = applied !== ALL_PROJECTS;
	const actions: MenuAction[] = [
		{ id: ALL_PROJECTS, title: "All projects" },
		...projects.map((project) => ({ id: project.id, title: project.name })),
	].map((action) => ({ ...action, state: action.id === applied ? "on" : "off" }));

	const choose = ({ nativeEvent: { event } }: NativeActionEvent) => {
		// Compared with the saved id, not the applied one: choosing All over a
		// project that no longer exists still has to clear it from storage.
		if (event === activeProjectId) return;
		if (event !== applied) haptics.select();
		// Rebuilding the list must not hold up the menu's dismissal.
		startTransition(() => setActiveProject(event));
	};

	const row = (
		<>
			<Text style={styles.label} numberOfLines={1}>
				Project
			</Text>
			{/* A long project name ellipsizes rather than pushing the label out. */}
			<View style={styles.value}>
				<Text style={[styles.valueText, scoped && { color: t.accent }]} numberOfLines={1}>
					{label}
				</Text>
				{/* Up and down, not an overflow "…": this picks a value from a list. */}
				<AppSymbol name="chevronUpDown" size={iconSize.sm} color={scoped ? t.accent : t.textTertiary} />
			</View>
		</>
	);
	const a11y = {
		accessibilityRole: "button" as const,
		accessibilityLabel: `Project, ${label}`,
		accessibilityHint: "Chooses which project's pull requests are listed",
	};

	// The whole row is the trigger, as a settings row with a pop-up value is.
	// It also keeps the menu's host as wide as the list: a host sized to a
	// shorter name holds that width, and would clip a longer one chosen later.
	return (
		<MenuView ref={menu} actions={actions} colorScheme={scheme} title="Show pull requests from" onPressAction={choose} testID="project-switcher">
			{Platform.OS === "android" ? (
				// The row claims the touch on Android, so it ripples and opens the
				// dropdown itself. On iOS the SwiftUI menu takes the tap.
				<Pressable {...a11y} android_ripple={feedback.android_ripple} onPress={() => menu.current?.show()} style={styles.row}>
					{row}
				</Pressable>
			) : (
				<View accessible {...a11y} style={styles.row}>
					{row}
				</View>
			)}
		</MenuView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		row: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.md,
			minHeight: touchTarget,
			paddingHorizontal: space.lg,
		},
		label: { ...type.subheadline, color: t.textSecondary, flexShrink: 0 },
		value: { flex: 1, minWidth: 0, flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.xs },
		valueText: { ...type.subheadline, fontFamily: "DMSans_600SemiBold", fontWeight: "600", color: t.textTertiary, flexShrink: 1 },
	});
