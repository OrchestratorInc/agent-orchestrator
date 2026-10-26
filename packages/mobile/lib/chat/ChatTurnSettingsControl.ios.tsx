import { Host } from "@expo/ui";
import { Button, HStack, Image, Menu, Picker, Section, Text, Toggle } from "@expo/ui/swift-ui";
import { accessibilityLabel, buttonStyle, contentShape, disabled as disabledModifier, font, foregroundStyle, frame, imageScale, lineLimit, padding, pickerStyle, shapes, tag, truncationMode } from "@expo/ui/swift-ui/modifiers";
import { useState } from "react";
import { StyleSheet } from "react-native";
import { haptics } from "../haptics";
import { HarnessImage } from "../spawn-composer-controls.ios";
import type { IOSSymbol } from "../symbols";
import { useTheme, useThemeState } from "../ThemeProvider";
import { space, touchTarget } from "../tokens";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { turnSettingsParts, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow, type TurnSettingTopic } from "./turnSettingsModel";

// SwiftUI's flexible frame cannot be handed infinity across the bridge; any
// width past the widest screen does the same job and lets the label fill its row.
const FILL = 10_000;

const topicSymbols: Record<Exclude<TurnSettingTopic, "permissions">, IOSSymbol> = {
	model: "square.stack.3d.up",
	effort: "gauge.medium",
	fast: "bolt",
	setting: "slider.horizontal.3",
};

export function ChatTurnSettingsControl({ snapshot, models, options, disabled, onSettings, onOption, onOpenFallback }: ChatTurnSettingsControlProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const rows = turnSettingsRows(snapshot, models, options);
	// The approval mode is its own control, as on the spawn sheet, so it is
	// never read as part of the model.
	const approvalRow = rows.find((row) => turnSettingTopic(row) === "permissions");
	const modelRows = rows.filter((row) => row !== approvalRow);
	const parts = turnSettingsParts(snapshot, models, options);
	const summary = [parts.model, parts.effort].filter(Boolean).join(" · ");
	// A SwiftUI picker keeps showing the choice it was given when the save fails
	// and the setting stays put. Rebuilding the menu then shows the setting as
	// saved; the menu has long closed, so nothing visibly moves.
	const [generation, setGeneration] = useState(0);

	const choose = (row: TurnSettingRow, choice: string | boolean) => {
		haptics.select();
		const write = turnSettingWrite(snapshot.settings, row, choice);
		const saved = write.kind === "option" ? onOption(write.id, write.value) : onSettings(write.settings);
		void saved.then(() => {}, () => setGeneration((value) => value + 1));
	};

	// The model's whole stretch of the row is its menu's own label, so every
	// point of it opens the menu.
	const label = (
		<HStack spacing={space.xs} modifiers={[frame({ maxWidth: FILL, minHeight: touchTarget, alignment: "leading" }), contentShape(shapes.rectangle())]}>
			{/* The harness's mark beside its model, as the spawn sheet names it. */}
			{snapshot.harness ? <HarnessImage harness={snapshot.harness} /> : null}
			<Text modifiers={[font({ textStyle: "footnote", weight: "semibold" }), foregroundStyle(t.textSecondary), lineLimit(1), truncationMode("tail")]}>{summary || "Turn settings"}</Text>
			<Image systemName="chevron.up.chevron.down" color={t.textSecondary} modifiers={[font({ textStyle: "footnote", weight: "semibold" }), imageScale("small")]} />
		</HStack>
	);
	const modifiers = [
		buttonStyle("plain"),
		padding({ horizontal: space.sm }),
		accessibilityLabel(summary ? `Turn settings, ${summary}` : "Turn settings"),
		disabledModifier(Boolean(disabled)),
	];
	const approvalSelection = approvalRow?.kind === "boolean" ? undefined : approvalRow?.choices.find((choice) => choice.selected);

	return (
		<Host style={styles.host} ignoreSafeArea="all" colorScheme={scheme}>
			<HStack spacing={space.none}>
				<Menu key={generation} label={label} modifiers={modifiers}>
					<Section title="Turn settings">
						{modelRows.map((row) => <SettingPicker key={row.id} row={row} onChoose={(choice) => choose(row, choice)} />)}
					</Section>
					{/* The sheet adds what a menu cannot: each choice's description and a refresh. */}
					<Section>
						<Button label="All turn settings" systemImage={topicSymbols.setting} onPress={onOpenFallback} />
					</Section>
				</Menu>
				{approvalRow && approvalRow.kind !== "boolean" ? (
					<Menu
						key={`approval-${generation}`}
						label={
							<HStack spacing={space.xxs} modifiers={[frame({ minHeight: touchTarget }), contentShape(shapes.rectangle())]}>
								{/* One native asset stays crisp at every display scale, where a
								    ring and a check drawn as two symbols would not line up. */}
								<Image assetName="CircleDashedCheck" modifiers={[imageScale("small")]} />
								<Text modifiers={[font({ textStyle: "footnote", weight: "semibold" }), foregroundStyle(t.textSecondary), lineLimit(1)]}>{parts.approval || approvalRow.value}</Text>
								<Image systemName="chevron.up.chevron.down" color={t.textSecondary} modifiers={[font({ textStyle: "footnote", weight: "semibold" }), imageScale("small")]} />
							</HStack>
						}
						modifiers={[
							buttonStyle("plain"),
							padding({ horizontal: space.sm }),
							accessibilityLabel(`${approvalRow.label}, ${approvalRow.value}`),
							disabledModifier(Boolean(disabled)),
						]}
					>
						<Section title={approvalRow.label}>
							{approvalRow.choices.map((choice) => (
								<Toggle
									key={choice.value}
									label={choice.label}
									isOn={choice.value === approvalSelection?.value}
									onIsOnChange={(on) => { if (on) choose(approvalRow, choice.value); }}
								/>
							))}
						</Section>
					</Menu>
				) : null}
			</HStack>
		</Host>
	);
}

/**
 * One setting as a submenu: a toggle for an on/off option, otherwise a picker,
 * which shows the current choice under its title and ticks it inside, and which
 * VoiceOver announces as selected.
 */
function SettingPicker({ row, onChoose }: { row: TurnSettingRow; onChoose(choice: string | boolean): void }) {
	const topic = turnSettingTopic(row);
	if (row.kind === "boolean") {
		return <Toggle label={row.label} systemImage={topic && topic !== "permissions" ? topicSymbols[topic] : undefined} isOn={Boolean(row.enabled)} onIsOnChange={onChoose} />;
	}
	const choices = row.choices.map((choice) => <Text key={choice.value} modifiers={[tag(choice.value)]}>{choice.label}</Text>);
	const selection = row.choices.find((choice) => choice.selected)?.value ?? null;
	const onSelectionChange = (value: string | null) => {
		if (value !== null) onChoose(value);
	};
	return (
		<Picker label={row.label} systemImage={topic && topic !== "permissions" ? topicSymbols[topic] : undefined} selection={selection} onSelectionChange={onSelectionChange} modifiers={[pickerStyle("menu")]}>
			{choices}
		</Picker>
	);
}

const styles = StyleSheet.create({
	host: { alignSelf: "stretch", height: touchTarget },
});
