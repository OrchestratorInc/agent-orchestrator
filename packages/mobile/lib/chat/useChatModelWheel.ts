import { useCallback, useMemo, useRef } from "react";
import { AUTOMATIC_MODEL } from "../agentSwitch";
import { cloudAgentLabel } from "../cloud/agents";

import { useEffortSliderSwitch } from "../useEffortSlider";
import type { WheelItem } from "../wheel-picker";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { splitModelChoices, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";

/** Names for agents whose label is only their id. */
const AGENT_NAMES: Record<string, string> = { copilot: "Copilot", gemini: "Gemini", aider: "Aider", amp: "Amp", goose: "Goose", qwen: "Qwen" };

/** An agent's name for the wheel: its label, unless that is only its id. */
export function agentName(id: string | undefined, label?: string): string {
	// The wheel is narrow and the agent's maker says enough: "Claude", not "Claude Code".
	if (id === "claude-code") return "Claude";
	if (label && label !== id) return label;
	if (!id) return "Agent";
	const known = cloudAgentLabel(id);
	if (known !== id) return known;
	return AGENT_NAMES[id] ?? id.split(/[-_]/).map((part) => (part ? part[0].toUpperCase() + part.slice(1) : part)).join(" ");
}

/** The listed model an agent runs by default, by the catalog's default label; none when it lists no such model. */
function defaultModelOf(otherAgents: ChatTurnSettingsControlProps["otherAgents"], agentId: string): string | undefined {
	const list = otherAgents?.models[agentId];
	if (!list?.defaultLabel) return undefined;
	return [...list.models, ...(list.older ?? [])].find((model) => model.label === list.defaultLabel)?.id;
}

/** An agent's column entry: its id, its name, and the models the model wheel lists for it. */
export type WheelAgent = { id: string; label: string; models: WheelItem[] };

/** A provider setting shown under the wheels: an on/off, or a choice stepped through. */
export type WheelSetting = { id: string; label: string; kind: "boolean"; on: boolean } | { id: string; label: string; kind: "select"; value: string };

/**
 * The chat's model, picked on two wheels over the keyboard as iOS's date
 * picker picks the time: the agent, then that agent's models, newest of each
 * family first and older versions after them. The running agent's models are
 * the conversation's; another agent's are the ones it would start with, and
 * choosing one only records the switch (`otherAgents.onPick`), which runs with
 * the next message, as the model menu's "Other agents" did. Choosing the
 * running agent again drops that switch. The provider's other settings are
 * rows under the wheels; Fast mode among them only while the effort slider,
 * which carries it as a bolt, is not shown (`fastOnEffort`).
 *
 * Whether the wheels are open lives in a switch, as the effort slider's does.
 */
export function useChatModelWheel({ snapshot, models, options, disabled, onSettings, onOption, otherAgents, fastOnEffort = false }: ChatTurnSettingsControlProps & { fastOnEffort?: boolean }) {
	const rows = useMemo(() => turnSettingsRows(snapshot, models, options), [snapshot, models, options]);
	const modelRow = rows.find((row) => row.kind === "select" && turnSettingTopic(row) === "model");
	const pending = otherAgents?.pending;
	const current = snapshot.harness;

	const agents = useMemo<WheelAgent[]>(() => {
		const split = splitModelChoices(modelRow?.choices ?? []);
		const own: WheelAgent = {
			id: current,
			label: agentName(current),
			// The model the agent runs when none is picked carries the star.
			models: [...split.latest, ...split.older].map((choice) => ({ key: choice.value, label: choice.label, starred: models.find((model) => model.id === choice.value)?.default === true })),
		};
		const others = (otherAgents?.agents ?? []).map((agent) => {
			const list = otherAgents?.models[agent.id];
			const listed = [...(list?.models ?? []), ...(list?.older ?? [])];
			// Its default starred where the catalog lists it; a "Default" row of its
			// own only when the default is none of the models listed.
			const starredId = defaultModelOf(otherAgents, agent.id);
			return {
				id: agent.id,
				label: agentName(agent.id, agent.label),
				models: [
					...(starredId ? [] : [{ key: AUTOMATIC_MODEL, label: list?.defaultLabel ?? "Default", starred: true }]),
					...listed.map((model) => ({ key: model.id, label: model.label, starred: model.id === starredId })),
				],
			};
		});
		return current && own.models.length ? [own, ...others] : others;
	}, [modelRow, models, current, otherAgents?.agents, otherAgents?.models]);

	// Where the wheels stand: the switch picked, else the running agent's model.
	const chosenAgent = pending?.harness ?? current;
	// A switch to an agent's own default stands on its starred model.
	const chosenModel = pending
		? pending.model ?? defaultModelOf(otherAgents, pending.harness) ?? AUTOMATIC_MODEL
		: modelRow?.choices.find((choice) => choice.selected)?.value;

	const settings = useMemo<WheelSetting[]>(() => rows
		.filter((row) => row !== modelRow && !["model", "effort", "permissions"].includes(turnSettingTopic(row) ?? ""))
		.filter((row) => !(fastOnEffort && row.kind === "boolean" && turnSettingTopic(row) === "fast"))
		.map((row) => row.kind === "boolean"
			? { id: row.id, label: row.label, kind: "boolean" as const, on: Boolean(row.enabled) }
			: { id: row.id, label: row.label, kind: "select" as const, value: row.value }), [rows, modelRow, fastOnEffort]);

	const latest = useRef({ snapshot, rows, modelRow, pending, otherAgents, onSettings, onOption, agents });
	latest.current = { snapshot, rows, modelRow, pending, otherAgents, onSettings, onOption, agents };

	const write = (row: TurnSettingRow, value: string | boolean) => {
		const { snapshot: snap, onOption: option, onSettings: save } = latest.current;
		const change = turnSettingWrite(snap.settings, row, value);
		void (change.kind === "option" ? option(change.id, change.value) : save(change.settings)).catch(() => {});
	};

	/** The wheels came to rest on this agent and model. */
	const choose = useCallback((agentId: string, modelKey: string) => {
		const { snapshot: snap, modelRow: row, pending: held, otherAgents: switching, agents: list } = latest.current;
		if (agentId === snap.harness) {
			// The running agent again: no switch, and its model saved if it changed.
			if (held) switching?.onPick(undefined);
			if (row && !row.choices.find((choice) => choice.value === modelKey)?.selected) write(row, modelKey);
			return;
		}
		const model = list.find((agent) => agent.id === agentId)?.models.find((item) => item.key === modelKey);
		if (!model || (held?.harness === agentId && (held.model ?? AUTOMATIC_MODEL) === modelKey)) return;
		const defaultLabel = switching?.models[agentId]?.defaultLabel;
		switching?.onPick({
			harness: agentId,
			model: modelKey === AUTOMATIC_MODEL ? undefined : modelKey,
			modelLabel: modelKey === AUTOMATIC_MODEL ? defaultLabel ?? "Default model" : model.label,
		});
	}, []);

	/** A setting's row tapped: an on/off flips, a choice steps to the next. */
	const toggleSetting = useCallback((id: string) => {
		const row = latest.current.rows.find((item) => item.id === id);
		if (!row) return;
		if (row.kind === "boolean") { write(row, !row.enabled); return; }
		const at = row.choices.findIndex((choice) => choice.selected);
		const next = row.choices[(at + 1) % row.choices.length];
		if (next) write(row, next.value);
	}, []);

	const slider = useEffortSliderSwitch();
	return {
		agents,
		chosenAgent,
		chosenModel,
		settings,
		/** Switching agents waits while the agent works. */
		agentsLocked: Boolean(otherAgents?.disabled),
		slider,
		busy: Boolean(disabled),
		choose,
		toggleSetting,
	};
}
