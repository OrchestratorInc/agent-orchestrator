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

export type AgentSwitchTransition = { sourceHarness?: string; targetHarness?: string; sourceModel?: string; targetModel?: string; targetReasoningEffort?: string };

export function agentSwitchTarget(transition: unknown): string | undefined {
	const target = (transition as AgentSwitchTransition | undefined)?.targetHarness;
	return target || undefined;
}

export function useIsCloudSessionCreator(session: WorkspaceSession | undefined): boolean {
	const meUserId = useCloudMeUserId(Boolean(session?.cloud));
	return Boolean(meUserId && session?.cloud?.createdByUserId === meUserId);
}

export function useCloudAgentSwitch(session: WorkspaceSession | undefined) {
	const orgId = session?.cloud?.orgId;
	const { client } = useCloudCp(Boolean(orgId));
	const offering = useCloudOffering(Boolean(orgId), orgId);
	const connections = useProviderConnections(Boolean(orgId)).data;
	const transitions = useSessionInterfaceTransition(orgId ? session?.id : undefined, orgId ? { orgId } : undefined);
	const mode: "chat" | "tui" = session?.mode === "chat" ? "chat" : "tui";
	const provider = session?.provider;
	const creator = useIsCloudSessionCreator(orgId ? session : undefined);
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
		available: Boolean(orgId && creator && session?.isTerminated !== true && agents.some((agent) => agent.id !== provider)),
		agents,
		switching,
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
