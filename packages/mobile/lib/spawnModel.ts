export type SpawnModelSource = { projectId: string | null; agentId: string };

/** `rankedAgents` is already in the default order (see rankAgents), so the first one wins. */
export function resolveSpawnAgent(rankedAgents: readonly string[]): string {
	return rankedAgents[0] ?? "";
}

export function spawnModelSourceChanged(current: SpawnModelSource, next: SpawnModelSource): boolean {
	return current.projectId !== next.projectId || current.agentId !== next.agentId;
}

/**
 * What to send as the model.
 *
 * A picked value is sent even when the catalog marks it as default. The old rule
 * dropped it as redundant, so choosing the model the sheet had *shown* as the
 * default sent no model at all: the session recorded nothing, and the chat had no
 * way to know what was asked for, so it displayed whatever the thread happened to
 * be running. Sending the pick is what makes "you started this with Astra" true
 * in both places.
 *
 * `touched` is false for the untouched "Automatic" state, which sends nothing and
 * lets the provider choose — the one case where no model is the honest answer.
 */
export function modelOverride(value: string, touched: boolean): string | undefined {
	if (!touched) return undefined;
	const clean = value.trim();
	return clean && clean !== "__auto__" ? clean : undefined;
}
