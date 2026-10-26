import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useRef } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { iconSize, space, stateOpacity, touchTarget, type } from "../tokens";
import { chatMenuIcon } from "./chatMenuIcons";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { turnSettingsRows, turnSettingsSummary, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";

/**
 * The composer's turn settings as Compose's dropdown: one submenu per setting,
 * its choices ticked, and an on/off option as a checkable item. A submenu has
 * no subtitle on Android, so its title carries the current value.
 */
export function ChatTurnSettingsControl({ snapshot, models, options, disabled, onSettings, onOption, onOpenFallback }: ChatTurnSettingsControlProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const feedback = usePressFeedback("row");
	const menu = useRef<MenuComponentRef>(null);
	const rows = turnSettingsRows(snapshot, models, options);
	const summary = turnSettingsSummary(snapshot, models, options);

	const choose = (event: NativeActionEvent) => {
		if (event.nativeEvent.event === ALL_SETTINGS) {
			onOpenFallback();
			return;
		}
		const [rowIndex, choiceIndex] = event.nativeEvent.event.split(":").map(Number);
		const row = rows[rowIndex];
		if (!row) return;
		const choice = row.kind === "boolean" ? !row.enabled : row.choices[choiceIndex]?.value;
		if (choice === undefined) return;
		haptics.select();
		const write = turnSettingWrite(snapshot.settings, row, choice);
		if (write.kind === "option") void onOption(write.id, write.value).catch(() => {});
		else void onSettings(write.settings).catch(() => {});
	};

	return (
		<View style={styles.control}>
			{/* The dropdown hangs from this sliver at the row's leading edge. The
			    visible row below is plain React Native, so it truncates like any
			    other label, and it opens the menu itself. */}
			<View pointerEvents="none" importantForAccessibility="no-hide-descendants" style={styles.anchor}>
				<MenuView ref={menu} colorScheme={scheme} actions={[...rows.map(menuAction), allSettings]} onPressAction={choose} testID="turn-settings-menu">
					<View style={styles.anchorBox} />
				</MenuView>
			</View>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={summary ? `Turn settings, ${summary}` : "Turn settings"}
				accessibilityState={{ disabled }}
				disabled={disabled}
				android_ripple={feedback.android_ripple}
				onPress={() => menu.current?.show()}
				style={({ pressed }) => [styles.trigger, pressed && feedback.pressed, disabled && styles.disabled]}
			>
				<Text numberOfLines={1} style={styles.label}>{summary || "Turn settings"}</Text>
				<AppSymbol name="chevronUpDown" size={iconSize.sm} color={t.textTertiary} />
			</Pressable>
		</View>
	);
}

const ALL_SETTINGS = "all-settings";

// The sheet adds what a menu cannot: each choice's description, the effort
// slider and a refresh.
const allSettings: MenuAction = { id: ALL_SETTINGS, title: "All turn settings", image: chatMenuIcon("setting") };

function menuAction(row: TurnSettingRow, rowIndex: number): MenuAction {
	const topic = turnSettingTopic(row);
	const image = topic ? chatMenuIcon(topic) : undefined;
	if (row.kind === "boolean") return { id: `${rowIndex}`, title: row.label, image, state: row.enabled ? "on" : "off" };
	return {
		id: `${rowIndex}`,
		title: `${row.label} · ${row.value}`,
		image,
		subactions: row.choices.map((choice, choiceIndex) => ({
			id: `${rowIndex}:${choiceIndex}`,
			title: choice.label,
			state: choice.selected ? "on" : "off",
		})),
	};
}

const makeStyles = (t: Theme) => StyleSheet.create({
	// Stretched, not content-sized: the label is the longest thing in the composer's
	// meta row, and a content-sized box let it decide how wide it wanted to be
	// rather than how wide it was allowed. `overflow: "hidden"` is the backstop —
	// with it, a label that fails to truncate is clipped instead of drawn across the
	// context meter beside it.
	control: { alignSelf: "stretch", maxWidth: "100%", minHeight: touchTarget, justifyContent: "center", overflow: "hidden" },
	anchor: { position: "absolute", top: 0, left: 0, bottom: 0 },
	anchorBox: { width: 1, height: touchTarget },
	trigger: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.sm },
	label: { fontFamily: "DMSans_600SemiBold", flexShrink: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	disabled: { opacity: stateOpacity.disabled },
});
