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
};

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
