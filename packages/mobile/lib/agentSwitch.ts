// Switching a Chat session's agent, as desktop's AgentSwitchPicker does it.
// Pure — no React Native or Expo imports — so the rules stay unit-testable.
//
// Choosing another agent's model in the composer's model menu only records the
// choice. The switch runs with the next message: one POST to
// /sessions/{id}/provider-switch that switches the agent and then sends.
import type { RankedAgent } from "./agentPicker";

/** The menu's default-model row (spawn's "__auto__"): send no model, the agent picks. */
export const AUTOMATIC_MODEL = "__auto__";

/** The agent the next message goes to, when it is not the one running. */
export type PendingAgentSwitch = {
	harness: string;
	/** Empty starts the agent on its own default model. */
	model?: string;
	/** What the capsule shows for that model. */
	modelLabel: string;
	/** Start the agent in Fast mode, where that model has one (Codex's fast tier). */
	fastMode?: boolean;
	/**
	 * Ask for Claude Code's Ultracode, in the message that runs the switch and
	 * in every one after it (desktop's pending switch). Claude Code only.
	 */
	ultracode?: boolean;
};

/**
 * An agent's models that run in Fast mode, from its catalog's `supportsFast`,
 * and whether its default model does (the pick that names no model).
 */
export type AgentFastModels = { models: readonly string[]; byDefault: boolean };

/**
 * Whether the switch picked can start in Fast mode: its model, or the
 * agent's default when it names none, has a fast tier. Desktop offers Fast for
 * a pending switch the same way, and the daemon refuses it for any other.
 */
export function switchOffersFast(pick: PendingAgentSwitch, fast: Readonly<Record<string, AgentFastModels>> | undefined): boolean {
	const list = fast?.[pick.harness];
	if (!list) return false;
	const model = requestedSwitchModel(pick.model);
	return model ? list.models.includes(model) : list.byDefault;
}

/**
 * Another model picked for the same switch keeps what was turned on for it,
 * as desktop's otherAgentSource keeps it: Fast mode only where the new model
 * has it too, and Ultracode, which works on every Claude Code model. A pick of
 * another agent starts with neither.
 */
export function repickedSwitch(next: PendingAgentSwitch, held: PendingAgentSwitch | undefined, fast: Readonly<Record<string, AgentFastModels>> | undefined): PendingAgentSwitch {
	if (!held || held.harness !== next.harness) return next;
	return {
		...next,
		...(held.fastMode && switchOffersFast(next, fast) ? { fastMode: true } : {}),
		...(held.ultracode ? { ultracode: true } : {}),
	};
}

export type AgentSwitchTarget = { id: string; label: string };

/**
 * The agents a Chat session can be handed to: installed and usable (as the
 * new task's menu requires), with a Chat driver on this machine, and not the
 * one running. Desktop's CHAT_SWITCH_TARGETS is the same set, which the
 * daemon reports as `chatHarnesses`.
 */
export function agentSwitchTargets(agents: readonly RankedAgent[], current: string, chatHarnesses: readonly string[]): AgentSwitchTarget[] {
	return agents
		.filter((agent) => agent.id !== current && agent.selectable && chatHarnesses.includes(agent.id))
		.map((agent) => ({ id: agent.id, label: agent.label || agent.id }));
}

/** The `model` field for the switch, or undefined to let the agent choose. */
export function requestedSwitchModel(choice: string | undefined): string | undefined {
	const trimmed = choice?.trim();
	return trimmed && trimmed !== AUTOMATIC_MODEL ? trimmed : undefined;
}

/**
 * The pick still waiting for a message. A pick whose agent the chat now runs
 * is done, however the switch's answer went (desktop drops it the same way).
 */
export function pendingSwitchFor(chosen: PendingAgentSwitch | undefined, currentHarness: string | undefined): PendingAgentSwitch | undefined {
	return chosen && currentHarness && chosen.harness !== currentHarness ? chosen : undefined;
}
