import type { AgentCatalog, AgentModelCatalog, ProjectDetail } from "../api";
import { CLOUD_AGENT_LABELS, CLOUD_AGENTS, isAgentConnected } from "./agents";
import type { CloudChatModels, CloudProject, CloudProviderConnection } from "./types";

// The spawn sheet's machine picker offers Orchestrator Cloud beside the paired machines.
// With it selected, the sheet's agent, project and model pickers read these
// instead of a daemon's catalogs, in the shapes those pickers already take.

/**
 * Every cloud agent, with the ones this account has connected marked usable.
 * Orchestrator Cloud knows exactly which credentials it holds, so the rest are
 * "unauthorized", never "unknown": the picker keeps unknown agents selectable
 * (a machine that could not check), which would offer agents that cannot run.
 */
export function cloudAgentCatalog(connections: readonly CloudProviderConnection[]): AgentCatalog {
	const supported = CLOUD_AGENTS.map((id) => ({ id, label: CLOUD_AGENT_LABELS[id] }));
	const withAuth = supported.map((agent) => ({ ...agent, authStatus: isAgentConnected(connections, agent.id) ? "authorized" as const : "unauthorized" as const }));
	return {
		supported,
		installed: withAuth,
		authorized: withAuth.filter((agent) => agent.authStatus === "authorized"),
	};
}

/** A cloud project's worker defaults, where the daemon's project detail keeps them. */
export function cloudProjectDetail(project: CloudProject): ProjectDetail {
	const worker = project.config?.worker;
	return {
		id: project.id,
		name: project.displayName,
		agent: worker?.agent,
		config: { worker: { agent: worker?.agent, agentConfig: { model: worker?.agentConfig?.model } } },
	};
}

/** The models a new cloud task can pick for an agent. */
export function cloudModelCatalog(agentId: string, catalog: CloudChatModels, now: Date = new Date()): AgentModelCatalog {
	return {
		agentId,
		selectionMode: "catalog",
		allowCustom: false,
		// Each model's effort levels, so New task's effort control works as on a machine.
		models: catalog.models.map((model) => ({
			id: model.id,
			label: model.displayName,
			isDefault: model.default,
			...(model.efforts?.length ? { efforts: model.efforts } : {}),
			...(model.defaultEffort ? { defaultEffort: model.defaultEffort } : {}),
		})),
		source: catalog.source ?? "cloud",
		stale: false,
		fetchedAt: now.toISOString(),
	};
}

/** Picker value for "create a cloud project", which leaves the sheet for the project form. */
export const NEW_CLOUD_PROJECT = "__new_cloud_project__";
