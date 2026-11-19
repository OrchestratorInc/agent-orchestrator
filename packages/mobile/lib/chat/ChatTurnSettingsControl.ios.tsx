import { Host } from "@expo/ui";
import { HStack, Label, Menu, Picker, RNHostView, Section, Spacer, Text, Toggle } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, disabled as disabledModifier, pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { useState } from "react";
import { StyleSheet } from "react-native";
import { haptics } from "../haptics";
import { approvalSymbol } from "../approvalSymbol";
import { EffortGaugeButton } from "../effort-gauge";
import { ApprovalMenu, Choice, ModelCapsuleLabel, useTriggerModifiers } from "../model-controls.ios";
import { HarnessImage } from "../spawn-composer-controls.ios";
import { defaultModelChoice } from "../spawnModel";
import { AUTOMATIC_MODEL } from "../agentSwitch";
import type { IOSSymbol } from "../symbols";
import { useThemeState } from "../ThemeProvider";
import { space, touchTarget } from "../tokens";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { splitModelChoices, turnSettingsParts, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";

const topicSymbols: Partial<Record<NonNullable<ReturnType<typeof turnSettingTopic>>, IOSSymbol>> = {
	fast: "bolt",
	setting: "slider.horizontal.3",
};

/**
 * The chat's controls row, drawn as the new task's (see model-controls): the
 * model in a capsule, the effort as a speedometer beside it that opens the
 * composer's slider, and the approval
 * mode as its symbol at the row's end. The capsule's menu lists the agent's
 * models as the new task's lists each agent's, then any other setting the
 * provider offers, such as Fast mode.
 */
export function ChatTurnSettingsControl({ snapshot, models, options, disabled, onSettings, onOption, effort, otherAgents }: ChatTurnSettingsControlProps) {
	const { scheme } = useThemeState();
	const rows = turnSettingsRows(snapshot, models, options);
	const select = (topic: string) => rows.find((row) => row.kind === "select" && turnSettingTopic(row) === topic);
	const modelRow = select("model");
	const effortRow = select("effort");
	const approvalRow = select("permissions");
	const otherRows = rows.filter((row) => row !== modelRow && row !== effortRow && row !== approvalRow);
	const modelChoices = splitModelChoices(modelRow?.choices ?? []);
	const parts = turnSettingsParts(snapshot, models, options);
	const off = Boolean(disabled);
	// A SwiftUI menu keeps showing the choice it was given when the save fails
	// and the setting stays put. Rebuilding the menus then shows the setting as
	// saved; they have long closed, so nothing visibly moves.
	const [generation, setGeneration] = useState(0);

	const pending = otherAgents?.pending;
	const choose = (row: TurnSettingRow, choice: string | boolean) => {
		const write = turnSettingWrite(snapshot.settings, row, choice);
		const saved = write.kind === "option" ? onOption(write.id, write.value) : onSettings(write.settings);
		void saved.then(() => {}, () => setGeneration((value) => value + 1));
	};

	// The running agent's model chosen again keeps the running agent: the pick
	// drops, and the model is saved only if it changed (desktop's keepCurrent).
	const chooseCurrent = (row: TurnSettingRow, choice: { value: string; selected: boolean }) => {
		if (pending) otherAgents?.onPick(undefined);
		if (!choice.selected) choose(row, choice.value);
	};

	return (
		<Host style={styles.host} ignoreSafeArea="all" colorScheme={scheme}>
			{/* Who runs the turn and its effort at the row's start beside attach,
			    approvals at its end beside the round button; the space between
			    them gives way first. */}
			<HStack spacing={space.none}>
				<Menu
					key={`model-${generation}`}
					label={pending
						? <ModelCapsuleLabel mark={<HarnessImage harness={pending.harness} />} title={pending.modelLabel} />
						: <ModelCapsuleLabel mark={snapshot.harness ? <HarnessImage harness={snapshot.harness} /> : null} title={parts.model || "Model"} />}
					modifiers={useTriggerModifiers({ label: pending ? `Next message switches to ${otherAgents?.agents.find((agent) => agent.id === pending.harness)?.label ?? pending.harness}, ${pending.modelLabel}` : parts.model ? `Model, ${parts.model}` : "Model", id: "chat-model", disabled: off })}
				>
					{modelChoices.latest.map((choice) => <Choice key={choice.value} label={choice.label} selected={!pending && choice.selected} onChoose={() => chooseCurrent(modelRow!, choice)} />)}
					{/* Older versions one level down, as on Android: the newest of
					    each family is what most turns want. */}
					{modelChoices.older.length ? (
						<Section>
							<Menu label="Older models" systemImage="clock.arrow.circlepath">
								{modelChoices.older.map((choice) => <Choice key={choice.value} label={choice.label} selected={!pending && choice.selected} onChoose={() => chooseCurrent(modelRow!, choice)} />)}
							</Menu>
						</Section>
					) : null}
					{/* The other agents, each opening onto its models as in the new
					    task's menu; choosing one switches the session's agent. */}
					{otherAgents?.agents.length ? (
						<Section title="Other agents">
							{otherAgents.agents.map((agent) => {
								const list = otherAgents.models[agent.id];
								const choice = (model: { id: string; label: string }) => (
									<Choice
										key={model.id}
										label={model.label}
										selected={pending?.harness === agent.id && (pending.model ?? AUTOMATIC_MODEL) === model.id}
										onChoose={() => otherAgents.onPick({
											harness: agent.id,
											model: model.id === AUTOMATIC_MODEL ? undefined : model.id,
											modelLabel: model.id === AUTOMATIC_MODEL ? list?.defaultLabel ?? "Default model" : model.label,
										})}
									/>
								);
								return (
									<Menu key={agent.id} label={<Label title={agent.label} icon={<HarnessImage harness={agent.id} />} />} modifiers={[accessibilityIdentifier(`chat-agent-${agent.id}`), disabledModifier(Boolean(otherAgents.disabled))]}>
										{[{ id: AUTOMATIC_MODEL, label: defaultModelChoice(list?.defaultLabel) }, ...(list?.models ?? [])].map(choice)}
										{list?.older?.length ? (
											<Menu label="Older models" systemImage="clock.arrow.circlepath">
												{list.older.map(choice)}
											</Menu>
										) : null}
									</Menu>
								);
							})}
						</Section>
					) : null}
					{otherRows.length ? (
						<Section>
							{otherRows.map((row) => <SettingPicker key={row.id} row={row} onChoose={(choice) => { haptics.select(); choose(row, choice); }} />)}
						</Section>
					) : null}
				</Menu>
				{/* The effort as a speedometer lit to the chosen level; a tap
				    opens the slider over the composer. */}
				{effort?.choices.length ? (
					<RNHostView matchContents>
						<EffortGaugeButton choices={effort.choices} open={effort.open} onPress={effort.onToggle} disabled={off} testID="chat-effort" />
					</RNHostView>
				) : null}
				<Spacer minLength={0} />
				{approvalRow ? (
					<ApprovalMenu
						key={`approval-${generation}`}
						choices={approvalRow.choices.map((choice) => ({ value: choice.value, label: choice.label, symbol: approvalSymbol(choice.permissionMode, snapshot.harness), selected: choice.selected }))}
						onChoose={(value) => choose(approvalRow, value)}
						disabled={off}
						id="chat-approval"
					/>
				) : null}
			</HStack>
		</Host>
	);
}

/**
 * Any other setting as a submenu: a toggle for an on/off option, otherwise a
 * picker, which shows the current choice under its title and ticks it inside,
 * and which VoiceOver announces as selected.
 */
function SettingPicker({ row, onChoose }: { row: TurnSettingRow; onChoose(choice: string | boolean): void }) {
	const topic = turnSettingTopic(row);
	const systemImage = topic ? topicSymbols[topic] : undefined;
	if (row.kind === "boolean") {
		return <Toggle label={row.label} systemImage={systemImage} isOn={Boolean(row.enabled)} onIsOnChange={onChoose} />;
	}
	const choices = row.choices.map((choice) => <Text key={choice.value} modifiers={[tag(choice.value)]}>{choice.label}</Text>);
	const selection = row.choices.find((choice) => choice.selected)?.value ?? null;
	const onSelectionChange = (value: string | null) => {
		if (value !== null) onChoose(value);
	};
	return (
		<Picker label={row.label} systemImage={systemImage} selection={selection} onSelectionChange={onSelectionChange} modifiers={[pickerStyle("menu")]}>
			{choices}
		</Picker>
	);
}

const styles = StyleSheet.create({
	host: { alignSelf: "stretch", height: touchTarget },
});
