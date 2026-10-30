import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useRef, type ReactNode } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { iconSize, radius, space, stateOpacity, touchTarget, type } from "../tokens";
import { chatMenuIcon } from "./chatMenuIcons";
import { COMPOSER_DISC } from "./composerStyles";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { turnSettingsParts, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";

const effortGlyph = { ios: "gauge.medium", android: "speed" } as const;
const approvalGlyph = { ios: "checkmark.shield", android: "verified_user" } as const;

/**
 * The chat's controls row on Android, laid out as the new task's: the model in
 * a filled capsule, the effort as a speedometer beside it, and the approval
 * mode as its shield at the row's end. Each opens Compose's dropdown with its
 * choices ticked; the model's also holds any other setting the provider offers,
 * an on/off one as a checkable item and the rest one level down.
 */
export function ChatTurnSettingsControl({ snapshot, models, options, disabled, onSettings, onOption }: ChatTurnSettingsControlProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const rows = turnSettingsRows(snapshot, models, options);
	const select = (topic: string) => rows.find((row) => row.kind === "select" && turnSettingTopic(row) === topic);
	const modelRow = select("model");
	const effortRow = select("effort");
	const approvalRow = select("permissions");
	const otherRows = rows.filter((row) => row !== modelRow && row !== effortRow && row !== approvalRow);
	const parts = turnSettingsParts(snapshot, models, options);
	const off = Boolean(disabled);

	const choose = (row: TurnSettingRow, choice: string | boolean) => {
		haptics.select();
		const write = turnSettingWrite(snapshot.settings, row, choice);
		if (write.kind === "option") void onOption(write.id, write.value).catch(() => {});
		else void onSettings(write.settings).catch(() => {});
	};
	// A choice of one row, by its index; an on/off row flips.
	const chooseIn = (row: TurnSettingRow | undefined) => (index: number) => {
		if (!row) return;
		const choice = row.kind === "boolean" ? !row.enabled : row.choices[index]?.value;
		if (choice !== undefined) choose(row, choice);
	};

	const modelActions: MenuAction[] = [
		...(modelRow?.choices.map((choice, index) => ({ id: `model:${index}`, title: choice.label, state: choice.selected ? "on" as const : "off" as const })) ?? []),
		...otherRows.map((row, index) => otherAction(row, index)),
	];
	const onModelAction = (event: NativeActionEvent) => {
		const [kind, first, second] = event.nativeEvent.event.split(":");
		if (kind === "model") chooseIn(modelRow)(Number(first));
		else if (kind === "other") chooseIn(otherRows[Number(first)])(Number(second));
	};

	return (
		<View style={styles.row}>
			<MenuTrigger
				actions={modelActions}
				onAction={onModelAction}
				disabled={off}
				accessibilityLabel={parts.model ? `Model, ${parts.model}` : "Model"}
				testID="chat-model"
			>
				<View style={styles.capsule}>
					<Text numberOfLines={1} style={styles.label}>{parts.model || "Model"}</Text>
					<AppSymbol name="chevronUpDown" size={iconSize.sm} color={t.textTertiary} />
				</View>
			</MenuTrigger>
			{effortRow ? (
				<MenuTrigger
					actions={effortRow.choices.map((choice, index) => ({ id: `${index}`, title: choice.label, state: choice.selected ? "on" : "off" }))}
					onAction={(event) => chooseIn(effortRow)(Number(event.nativeEvent.event))}
					disabled={off}
					accessibilityLabel={`Effort, ${effortRow.value}`}
					testID="chat-effort"
				>
					<AppSymbol name={effortGlyph} size={iconSize.lg} color={t.textSecondary} />
				</MenuTrigger>
			) : null}
			<View style={styles.spacer} />
			{approvalRow ? (
				<MenuTrigger
					actions={approvalRow.choices.map((choice, index) => ({ id: `${index}`, title: choice.label, state: choice.selected ? "on" : "off" }))}
					onAction={(event) => chooseIn(approvalRow)(Number(event.nativeEvent.event))}
					disabled={off}
					accessibilityLabel={`${approvalRow.label}, ${approvalRow.value}`}
					testID="chat-approval"
				>
					<AppSymbol name={approvalGlyph} size={iconSize.lg} color={t.textSecondary} />
				</MenuTrigger>
			) : null}
		</View>
	);
}

/** Any other setting: an on/off one as a checkable item, the rest one level down with its value in the title. */
function otherAction(row: TurnSettingRow, index: number): MenuAction {
	const topic = turnSettingTopic(row);
	const image = topic && topic !== "model" ? chatMenuIcon(topic) : undefined;
	if (row.kind === "boolean") return { id: `other:${index}:0`, title: row.label, image, state: row.enabled ? "on" : "off" };
	return {
		id: `other:${index}`,
		title: `${row.label} · ${row.value}`,
		image,
		subactions: row.choices.map((choice, choiceIndex) => ({ id: `other:${index}:${choiceIndex}`, title: choice.label, state: choice.selected ? "on" : "off" })),
	};
}

/**
 * A button that opens its own dropdown. Compose's trigger is no accessibility
 * node and draws no ripple, so the button is React Native's, and it opens the
 * menu itself, as the attach button does.
 */
function MenuTrigger({ actions, onAction, disabled, accessibilityLabel, testID, children }: {
	actions: MenuAction[];
	onAction: (event: NativeActionEvent) => void;
	disabled: boolean;
	accessibilityLabel: string;
	testID: string;
	children: ReactNode;
}) {
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const feedback = usePressFeedback("icon");
	const menu = useRef<MenuComponentRef>(null);
	return (
		<MenuView ref={menu} colorScheme={scheme} actions={actions} onPressAction={onAction} testID={`${testID}-menu`}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				accessibilityState={{ disabled }}
				disabled={disabled}
				android_ripple={feedback.android_ripple}
				onPress={() => menu.current?.show()}
				testID={testID}
				style={({ pressed }) => [styles.trigger, pressed && feedback.pressed, disabled && styles.disabled]}
			>
				{children}
			</Pressable>
		</MenuView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	// Clipped, so a long model name gives way rather than painting over the round button.
	row: { alignSelf: "stretch", minHeight: touchTarget, flexDirection: "row", alignItems: "center", overflow: "hidden" },
	spacer: { flex: 1 },
	trigger: { minWidth: touchTarget, minHeight: touchTarget, alignItems: "center", justifyContent: "center", flexShrink: 1 },
	// The new task's capsule: filled, the composer's discs' height.
	capsule: { height: COMPOSER_DISC, maxWidth: "100%", flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.md, borderRadius: radius.pill, backgroundColor: t.bgSubtle },
	label: { fontFamily: "DMSans_600SemiBold", flexShrink: 1, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	disabled: { opacity: stateOpacity.disabled },
});
