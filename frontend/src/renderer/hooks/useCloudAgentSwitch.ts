import { useMemo } from "react";
import { switchEffort, type PendingAgentSwitch } from "../components/chat/AgentSwitchPicker";
import { appI18n } from "../i18n";
import { agentLabel } from "../lib/agent-options";
import { isCloudHarnessConnected } from "../lib/cloud-agents";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";
import { useCloudMeUserId } from "./useCloudMeUserId";
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
 * Whether the signed-in person started this Cloud session. Only its creator may
 * switch its agent or cancel that switch: the new agent runs on their login
 * for it, and the control plane refuses anyone else, an org admin included.
 */
export function useIsCloudSessionCreator(session: WorkspaceSession | undefined): boolean {
	const meUserId = useCloudMeUserId(Boolean(session?.cloud));
	return Boolean(meUserId && session?.cloud?.createdByUserId === meUserId);
}

/**
 * Switching a Cloud session's agent: the agents its picker offers and the
 * switch itself, the session's interface transition with a target harness. The
 * control plane lists the agents it can switch to (`/me` agentSwitchHarnesses);
 * Chat offers only those with a Chat, and only agents the person is signed in
 * to, as the new task composer does. The switch keeps the session's interface,
 * and its progress, held messages and failure notice are the interface
 * transition's. Chat drains its agent first. A terminal cannot prove it is
 * idle, so a drain would wait out its window and fail: as a switch from a
 * Cloud terminal to Chat does, the switch reads the agent's latest activity
 * and stops an idle terminal directly, and refuses while the agent works.
 * Only the session's creator is offered the switch; anyone else sees it run.
 */
export function useCloudAgentSwitch(session: WorkspaceSession | undefined) {
	const orgId = session?.cloud?.orgId;
	const { client } = useCloudCp(Boolean(orgId));
	const offering = useCloudOffering(Boolean(orgId), orgId);
	const connections = useProviderConnections(Boolean(orgId)).data;
	const transitions = useSessionInterfaceTransition(orgId ? session?.id : undefined, orgId ? { orgId } : undefined);
	const mode: "chat" | "tui" = session?.mode === "chat" ? "chat" : "tui";
	const provider = session?.provider;
	const creator = useIsCloudSessionCreator(orgId ? session : undefined);
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
		available: Boolean(orgId && creator && session?.isTerminated !== true && agents.some((agent) => agent.id !== provider)),
		agents,
		/** An agent switch (or any switch) is starting or running. */
		switching,
		/** The agent switch running now, for the transcript's "Switching" line. */
		running,
		start: async (pick: PendingAgentSwitch) => {
			const target = {
				targetMode: mode,
				targetHarness: pick.harness,
				...(pick.model ? { model: pick.model } : {}),
				...(switchEffort(pick) ? { reasoningEffort: switchEffort(pick) } : {}),
			};
			if (mode === "chat" || !orgId || !session) return transitions.start({ ...target, policy: "drain" });
			const { session: latest } = await client.getSession(orgId, session.id);
			if (["active", "waiting_input", "blocked"].includes(latest.activityState) ||
				latest.status === "working" || latest.status === "needs_input") {
				throw new Error(appI18n.t("agentSwitch.busy"));
			}
			return transitions.start({ ...target, policy: "interrupt" });
		},
	};
}
