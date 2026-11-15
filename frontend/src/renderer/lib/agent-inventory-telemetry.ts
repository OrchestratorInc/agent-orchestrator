import type { AgentReadiness } from "../hooks/useAgentReadinessQuery";

export type AgentInventory = {
  installed_count: number;
  authorized_count: number;
  supported_count: number;
  authorized_agents: string;
};

const MAX_AGENT_LIST_LENGTH = 200;

export function buildAgentInventory(catalog: AgentReadiness): AgentInventory {
  const authorized = catalog.agents
    .filter((agent) => agent.authentication.state === "authorized")
    .map((agent) => agent.id)
    .filter((id): id is string => typeof id === "string" && id !== "")
    .sort();
  const joined = authorized.join(",");
  return {
    installed_count: catalog.agents.filter((agent) => agent.installation.state === "installed").length,
    authorized_count: authorized.length,
    supported_count: catalog.agents.length,
    authorized_agents: joined.slice(0, MAX_AGENT_LIST_LENGTH),
  };
}

export type SetupCheck = {
  git?: boolean;
  tmux?: boolean;
  gh?: boolean;
  gh_signed_in?: boolean;
  agents_installed: number;
  agents_signed_in: number;
  agents_installed_ids: string;
  agents_signed_in_ids: string;
};

const AGENT_ID_PATTERN = /^[a-z][a-z0-9-]{0,39}$/;

function agentIds(catalog: AgentReadiness, keep: (agent: AgentReadiness["agents"][number]) => boolean): string[] {
  return catalog.agents
    .filter(keep)
    .map((agent) => agent.id)
    .filter((id): id is string => typeof id === "string" && AGENT_ID_PATTERN.test(id))
    .sort();
}

export function buildSetupCheck(
  requirements: readonly { id: string; satisfied: boolean }[],
  catalog: AgentReadiness,
  ghSignedIn: boolean | undefined,
): SetupCheck {
  const satisfied = (id: string) => requirements.find((requirement) => requirement.id === id)?.satisfied;
  const installed = agentIds(catalog, (agent) => agent.installation.state === "installed");
  const signedIn = agentIds(catalog, (agent) => agent.authentication.state === "authorized");
  const check: SetupCheck = {
    agents_installed: installed.length,
    agents_signed_in: signedIn.length,
    agents_installed_ids: installed.join(",").slice(0, MAX_AGENT_LIST_LENGTH),
    agents_signed_in_ids: signedIn.join(",").slice(0, MAX_AGENT_LIST_LENGTH),
  };
  for (const id of ["git", "tmux", "gh"] as const) {
    const value = satisfied(id);
    if (value !== undefined) check[id] = value;
  }
  if (ghSignedIn !== undefined) check.gh_signed_in = ghSignedIn;
  return check;
}
