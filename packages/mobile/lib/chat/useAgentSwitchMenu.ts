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
		setModels({});
		if (!cfg || !targets) return;
		let cancelled = false;
		// getAgentModels keeps polling while an agent is still discovering its
		// catalog: each agent's models show as soon as they arrive, so one slow
		// agent does not hold the others back, and leaving stops the polling.
		const abort = new AbortController();
		for (const id of targets.split("|")) {
			void getAgentModels(cfg, id, catalogProject, abort.signal)
				.catch(() => undefined)
				.then((catalog) => {
					if (cancelled) return;
					setModels((current) => ({ ...current, [id]: menuModels(catalog?.models ?? [], AUTOMATIC_MODEL) }));
				});
		}
		return () => { cancelled = true; abort.abort(); };
	}, [catalogProject, cfg, targets]);

	return useMemo(() => (agents?.length ? { agents, models } : undefined), [agents, models]);
}
