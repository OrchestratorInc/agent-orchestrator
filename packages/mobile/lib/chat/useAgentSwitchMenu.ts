import { useEffect, useMemo, useState } from "react";
import { rankAgents } from "../agentPicker";
import { AUTOMATIC_MODEL, agentSwitchTargets, type AgentSwitchTarget } from "../agentSwitch";
import { getAgentModels, getAgents, getSettings } from "../api";
import type { ServerConfig } from "../config";
import { STANDALONE_PROJECT } from "../projectFilter";
import type { SpawnAgentModels } from "../spawn-composer-controls.types";
import { menuModels } from "../spawnModel";

export type AgentSwitchMenu = {
	/** The agents the session can be handed to; never the running one. */
	agents: AgentSwitchTarget[];
	/** Each agent's models as the new task's menu lists them. */
	models: Readonly<Record<string, SpawnAgentModels>>;
};

/**
 * The model menu's "Other agents": every agent this Chat can be handed to,
 * each with its models listed as the new task's menu lists them. Loaded when
 * the screen opens so the native menu shows them at once.
 */
export function useAgentSwitchMenu({ cfg, current, projectId, enabled }: {
	cfg: ServerConfig | null;
	current: string;
	projectId?: string;
	enabled: boolean;
}): AgentSwitchMenu | undefined {
	const [agents, setAgents] = useState<AgentSwitchTarget[]>();
	const [models, setModels] = useState<Record<string, SpawnAgentModels>>({});
	const catalogProject = projectId && projectId !== STANDALONE_PROJECT ? projectId : undefined;

	useEffect(() => {
		if (!cfg || !enabled || !current) { setAgents(undefined); return; }
		let cancelled = false;
		Promise.all([getAgents(cfg), getSettings(cfg)])
			.then(([catalog, settings]) => { if (!cancelled) setAgents(agentSwitchTargets(rankAgents(catalog), current, settings.chatHarnesses)); })
			.catch(() => { if (!cancelled) setAgents(undefined); });
		return () => { cancelled = true; };
	}, [cfg, current, enabled]);

	const targets = useMemo(() => (agents ?? []).map((agent) => agent.id).join("|"), [agents]);
	useEffect(() => {
		if (!cfg || !targets) { setModels({}); return; }
		let cancelled = false;
		void Promise.all(targets.split("|").map(async (id) => {
			const catalog = await getAgentModels(cfg, id, catalogProject).catch(() => undefined);
			return [id, menuModels(catalog?.models ?? [], AUTOMATIC_MODEL)] as const;
		})).then((entries) => { if (!cancelled) setModels(Object.fromEntries(entries)); });
		return () => { cancelled = true; };
	}, [catalogProject, cfg, targets]);

	return useMemo(() => (agents?.length ? { agents, models } : undefined), [agents, models]);
}
