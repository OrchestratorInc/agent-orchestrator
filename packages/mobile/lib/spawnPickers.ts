import { useCallback, useEffect, useMemo, useRef } from "react";
import { AUTOMATIC_MODEL } from "./agentSwitch";
import { approvalChoiceCopy, approvalLook } from "./chat/approvalCopy";
import type { ApprovalMode, ChatConfigOption } from "./chat/types";
import { permissionChoices as chatPermissionChoices } from "./chat/useChatPermissions";
import { agentName, type WheelAgent, type WheelSetting } from "./chat/useChatModelWheel";
import type { PermissionChoice } from "./permission-picker";
import type { SpawnComposerControlsProps } from "./spawn-composer-controls.types";
import { useEffortSliderSwitch } from "./useEffortSlider";

/** A new task has no provider settings to list under the wheels. */
const NO_SETTINGS: WheelSetting[] = [];

/**
 * Every agent's models as the chat's wheels list them: the agent's default
 * first, starred and named by the model it is ("Opus 5.5"), then the newest
 * of each family and the older versions after them.
 */
export function spawnWheelAgents(
	agents: SpawnComposerControlsProps["agents"],
	agentModels: SpawnComposerControlsProps["agentModels"],
): WheelAgent[] {
	return agents.map((agent) => {
		const list = agentModels[agent.id];
		return {
			id: agent.id,
			label: agentName(agent.id, agent.label),
			models: [
				{ key: AUTOMATIC_MODEL, label: list?.defaultLabel ?? "Default model", starred: true },
				...[...(list?.models ?? []), ...(list?.older ?? [])].map((model) => ({ key: model.id, label: model.label })),
			],
		};
	});
}

/** The approval modes a new task can start in, as the chat's permission picker lists them. */
export function spawnPermissionChoices(harness: string, modes: SpawnComposerControlsProps["approvalModes"], current: string, template?: readonly ChatConfigOption[]): PermissionChoice[] {
	// The agent's own modes by its own names, as the chat lists them, from the
	// settings it last reported in a chat (permissionChoices, the chat's own).
	// New task sends AO's mode, so each of the agent's choices stands for the
	// AO mode it applies; a mode it names none of keeps AO's words.
	if (template?.length) {
		const snapshot: Parameters<typeof chatPermissionChoices>[0] = { harness, settings: { approvalMode: current as ApprovalMode }, capabilities: ["config_options"], controller: { state: "ready" } };
		const named = chatPermissionChoices(snapshot, [], [...template]).choices;
		const offered = new Set(modes.map((mode) => mode.id));
		const own = named.filter((choice) => choice.permissionMode && offered.has(choice.permissionMode));
		if (own.length) {
			const ids = new Set(own.map((choice) => choice.permissionMode));
			return [
				...own.map((choice) => ({ value: choice.permissionMode!, label: choice.label, description: choice.description, selected: choice.permissionMode === current, look: choice.look })),
				...spawnPermissionChoices(harness, modes.filter((mode) => !ids.has(mode.id as ApprovalMode)), current),
			];
		}
	}
	return modes.map((mode) => ({
		value: mode.id,
		label: mode.label,
		description: approvalChoiceCopy(harness, mode.id as ApprovalMode)?.description,
		selected: mode.id === current,
		look: approvalLook(harness, mode.id as ApprovalMode),
	}));
}

/**
 * The new task's model wheels, effort slider and permission picker, the
 * chat's own, over the keyboard. Opening one puts the others away, and typing
 * puts them all away, as in chat.
 */
export function useSpawnPickers({ agents, agentModels, harness, modelSelection, onSelectAgentModel, approvalModes, approvalMode, onSelectApprovalMode, prompt, settingsTemplate }: Pick<
	SpawnComposerControlsProps,
	"agents" | "agentModels" | "harness" | "modelSelection" | "onSelectAgentModel" | "approvalModes" | "approvalMode" | "onSelectApprovalMode" | "prompt" | "settingsTemplate"
>) {
	const effortSlider = useEffortSliderSwitch();
	const modelSlider = useEffortSliderSwitch();
	const permissionSlider = useEffortSliderSwitch();
	useEffect(() => {
		effortSlider.setOpen(false);
		modelSlider.setOpen(false);
		permissionSlider.setOpen(false);
	}, [prompt, effortSlider, modelSlider, permissionSlider]);

	const wheelAgents = useMemo(() => spawnWheelAgents(agents, agentModels), [agents, agentModels]);
	const latest = useRef({ harness, modelSelection, onSelectAgentModel, approvalMode, onSelectApprovalMode });
	latest.current = { harness, modelSelection, onSelectAgentModel, approvalMode, onSelectApprovalMode };
	// The wheels choose once, as they close; the pair already in force changes nothing.
	const chooseModel = useCallback((agentId: string, modelKey: string) => {
		const { harness: current, modelSelection: model, onSelectAgentModel: select } = latest.current;
		if (agentId === current && modelKey === model) return;
		select(agentId, modelKey);
	}, []);
	const permissionChoices = useMemo(() => spawnPermissionChoices(harness, approvalModes, approvalMode, settingsTemplate), [harness, approvalModes, approvalMode, settingsTemplate]);
	const choosePermission = useCallback((value: string) => {
		const { approvalMode: current, onSelectApprovalMode: select } = latest.current;
		if (value !== current) select(value);
	}, []);
	const noSetting = useCallback(() => {}, []);

	return {
		effortSlider,
		model: {
			slider: modelSlider,
			agents: wheelAgents,
			chosenAgent: harness,
			chosenModel: modelSelection,
			runningAgent: harness,
			runningModel: modelSelection,
			settings: NO_SETTINGS,
			agentsLocked: false,
			choose: chooseModel,
			toggleSetting: noSetting,
		},
		permissions: { slider: permissionSlider, choices: permissionChoices, choose: choosePermission },
		/** For a trigger: put the other two away. */
		closeAllBut: (keep: "effort" | "model" | "permissions") => {
			if (keep !== "effort") effortSlider.setOpen(false);
			if (keep !== "model") modelSlider.setOpen(false);
			if (keep !== "permissions") permissionSlider.setOpen(false);
		},
	};
}
