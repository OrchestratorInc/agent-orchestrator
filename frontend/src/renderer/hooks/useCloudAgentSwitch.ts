import { useMemo } from "react";
import { switchEffort, type PendingAgentSwitch } from "../components/chat/AgentSwitchPicker";
import { agentLabel } from "../lib/agent-options";
import { isCloudHarnessConnected } from "../lib/cloud-agents";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudOffering } from "./useCloudOffering";
import { useProviderConnections } from "./useProviderConnections";
import { interfaceTransitionIsActive, useSessionInterfaceTransition } from "./useSessionInterfaceTransition";

/** A transition that switches a Cloud session's agent, as the status reports it. */
export type AgentSwitchTransition = { sourceHarness?: string; targetHarness?: string; sourceModel?: string; targetModel?: string; targetReasoningEffort?: string };

/** The agent a transition switches the session to; undefined for an interface-only switch. */
export function agentSwitchTarget(transition: unknown): string | undefined {
	const target = (transition as AgentSwitchTransition | undefined)?.targetHarness;
	return target || undefined;
}

/**
 * Switching a Cloud session's agent: the agents its picker offers and the
 * switch itself, the session's interface transition with a target harness. The
 * control plane lists the agents it can switch to (`/me` agentSwitchHarnesses);
 * Chat offers only those with a Chat, and only agents the person is signed in
 * to, as the new task composer does. The switch keeps the session's interface,
 * and its progress, held messages and failure notice are the interface
 * transition's.
 */
export function useCloudAgentSwitch(session: WorkspaceSession | undefined) {
	const orgId = session?.cloud?.orgId;
	const offering = useCloudOffering(Boolean(orgId), orgId);
	const connections = useProviderConnections(Boolean(orgId)).data;
	const transitions = useSessionInterfaceTransition(orgId ? session?.id : undefined, orgId ? { orgId } : undefined);
	const mode = session?.mode === "chat" ? "chat" : "tui";
	const provider = session?.provider;
	// A test's offering (or a partial one) may leave the lists out: none can switch.
	const agentSwitchHarnesses = offering.agentSwitchHarnesses ?? [];
	const chatHarnesses = offering.chatHarnesses ?? [];
	const agents = useMemo(() => {
		if (!provider) return [];
		return agentSwitchHarnesses
			.filter((id) => mode !== "chat" || chatHarnesses.includes(id))
			.filter((id) => id === provider || isCloudHarnessConnected(connections, id))
			.map((id) => ({ id, label: agentLabel(id) }));
	}, [agentSwitchHarnesses, chatHarnesses, connections, mode, provider]);
	const transition = transitions.transition;
	const running = agentSwitchTarget(transition) && interfaceTransitionIsActive(transition)
		? transition as typeof transition & AgentSwitchTransition : undefined;
	const switching = transitions.starting || Boolean(running);
	return {
		/** The session can switch to another agent here. */
		available: Boolean(orgId && session?.isTerminated !== true && agents.some((agent) => agent.id !== provider)),
		agents,
		/** An agent switch (or any switch) is starting or running. */
		switching,
		/** The agent switch running now, for the transcript's "Switching" line. */
		running,
		start: (pick: PendingAgentSwitch) => transitions.start({
			targetMode: mode,
			policy: "drain",
			targetHarness: pick.harness,
			...(pick.model ? { model: pick.model } : {}),
			...(switchEffort(pick) ? { reasoningEffort: switchEffort(pick) } : {}),
		}),
	};
}
