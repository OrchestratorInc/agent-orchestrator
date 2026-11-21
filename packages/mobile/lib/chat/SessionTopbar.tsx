import { Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AgentLogo } from "../AgentLogo";
import { AppSymbol } from "../AppSymbol";
import { Feather } from "../icons";
import { OrchestratorIcon } from "../orchestrator-icon";
import type { SessionTopbarAction } from "../sessionTopbar";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, fontScaleCap, space } from "../tokens";

const BAR_HEIGHT = 44;
const TAB_HEIGHT = 32;
const ICON_BUTTON = 36;

/**
 * The desktop session view's top bar (SessionTabsTopbar + SessionTopbarActions),
 * drawn by the conversation itself:
 *
 *   [‹] [agent · title · ⋮]              [Orchestrator]
 *   [‹] [agent · Orchestrator · ⋮]       [Board] [+ Task]
 *
 * The session is a tab, as on the desktop: its agent, its title and its ⋮
 * menu (the conversation's actions). The worktree shell is in that menu
 * rather than a + beside the tab. The session's own actions sit flat at the
 * bar's end. A solid bar with a hairline under it; the conversation starts
 * below. The back chevron is the phone's way home, where the desktop keeps
 * its sidebar open beside the session.
 */
export function SessionTopbar({ title, harness, onBack, onMenu, onNewTab, actions }: {
	title: string;
	harness?: string | null;
	onBack(): void;
	/** The tab's ⋮: the conversation's actions. Absent until they are ready. */
	onMenu?(): void;
	/** The + beside the tab: a worktree shell, the desktop's new terminal tab. */
	onNewTab?(): void;
	actions: readonly SessionTopbarAction[];
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	return (
		<View style={[styles.bar, { paddingTop: insets.top, height: insets.top + BAR_HEIGHT }]} testID="session-topbar">
			<Pressable accessibilityRole="button" accessibilityLabel="Back" onPress={onBack} hitSlop={6} style={({ pressed }) => [styles.iconButton, pressed && styles.pressed]} testID="session-topbar-back">
				<AppSymbol name="back" size={18} color={t.textSecondary} />
			</Pressable>
			<View style={styles.tab} accessible accessibilityRole="header" accessibilityLabel={title}>
				<AgentLogo harness={harness} size={16} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.title} style={styles.tabTitle}>{title}</Text>
				{onMenu ? (
					<Pressable accessibilityRole="button" accessibilityLabel="Conversation actions" onPress={onMenu} hitSlop={8} style={({ pressed }) => [styles.tabMenu, pressed && styles.pressed]} testID="session-topbar-menu">
						<Feather name="more-vertical" size={15} color={t.textTertiary} />
					</Pressable>
				) : null}
			</View>
			{onNewTab ? (
				<Pressable accessibilityRole="button" accessibilityLabel="New shell" onPress={onNewTab} hitSlop={4} style={({ pressed }) => [styles.iconButton, pressed && styles.pressed]} testID="session-topbar-new-tab">
					<Feather name="plus" size={18} color={t.textSecondary} />
				</Pressable>
			) : null}
			<View style={styles.spacer} />
			{actions.map((action) => action.id === "new-task" ? (
				<Pressable key={action.id} accessibilityRole="button" accessibilityLabel={action.label} disabled={action.disabled} onPress={action.onPress} style={({ pressed }) => [styles.primary, { backgroundColor: t.accent }, pressed && styles.pressed]} testID={`session-topbar-${action.id}`}>
					<Feather name="plus" size={15} color={t.onAccent} />
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.primaryLabel, { color: t.onAccent }]}>Task</Text>
				</Pressable>
			) : (
				<Pressable key={action.id} accessibilityRole="button" accessibilityLabel={action.label} disabled={action.disabled} onPress={action.onPress} hitSlop={4} style={({ pressed }) => [styles.iconButton, pressed && styles.pressed, action.disabled && styles.disabled]} testID={`session-topbar-${action.id}`}>
					{action.id === "orchestrator" ? <OrchestratorIcon size={18} color={t.textSecondary} /> : <Feather name="layout-grid" size={18} color={t.textSecondary} />}
				</Pressable>
			))}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	bar: { flexDirection: "row", alignItems: "center", gap: space.xxs, paddingHorizontal: space.xs, backgroundColor: t.bgBase, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderDefault },
	iconButton: { width: ICON_BUTTON, height: ICON_BUTTON, borderRadius: 8, alignItems: "center", justifyContent: "center" },
	pressed: { backgroundColor: t.bgSubtle },
	disabled: { opacity: 0.45 },
	tab: { flexShrink: 1, minWidth: 0, height: TAB_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.xs, paddingLeft: space.sm, paddingRight: space.xxs, borderRadius: 9, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, backgroundColor: t.bgElevated },
	tabTitle: { flexShrink: 1, minWidth: 0, fontFamily: font.medium, fontSize: 14, color: t.textPrimary },
	tabMenu: { width: 24, height: 28, borderRadius: 6, alignItems: "center", justifyContent: "center" },
	spacer: { flex: 1, minWidth: space.xs },
	primary: { height: 28, flexDirection: "row", alignItems: "center", gap: space.hair, paddingHorizontal: space.sm, borderRadius: 8, marginLeft: space.xxs },
	primaryLabel: { fontFamily: font.medium, fontSize: 13 },
});
