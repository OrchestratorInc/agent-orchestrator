import type { ConversationSnapshot } from "./types";

export type TurnOptionCatalog<Models extends unknown[], Options extends unknown[]> = {
	models: Models | [];
	configOptions: Options | [];
};

/**
 * Provider controls first, AO's model list when the provider offers none.
 *
 * The snapshot's `config_options` capability used to pick one of the two, but a
 * hibernated or still-starting chat reports no capabilities, so the screen asked
 * `/models`. Claude answers that with an empty list, and the composer lost its
 * model until the screen was reopened. `/config-options` answers an empty list
 * for drivers without provider controls, so asking it first needs no capability.
 */
export async function loadTurnOptionCatalog<Models extends unknown[], Options extends unknown[]>({
	loadModels,
	loadConfigOptions,
}: {
	loadModels(): Promise<Models>;
	loadConfigOptions(): Promise<Options>;
}): Promise<TurnOptionCatalog<Models, Options>> {
	const configOptions = await loadConfigOptions();
	if (configOptions.length) return { models: [], configOptions };
	return { models: await loadModels(), configOptions: [] };
}

/**
 * Whether the provider is up to answer for its catalog. The catalog is read from
 * the running provider, so a chat opened while hibernated or connecting has to
 * ask again once this turns true; nothing else reports the wake.
 */
export function providerCatalogLive(snapshot: Pick<ConversationSnapshot, "controller">): boolean {
	return snapshot.controller.state === "ready" || snapshot.controller.state === "busy";
}

export type TurnOptionsLoad = { key: string; refresh: boolean };

/**
 * When a chat screen loads its catalog: once for each provider state it sees,
 * past the cache when the provider has just come up, because what was cached
 * was read while it could not answer. `live` is undefined before the first
 * snapshot; `previousKey` is the key of the last load.
 */
export function turnOptionsLoadFor(previousKey: string | undefined, sessionId: string, live: boolean | undefined): TurnOptionsLoad | undefined {
	if (live === undefined) return undefined;
	const key = `${sessionId}:${live}`;
	if (previousKey === key) return undefined;
	return { key, refresh: live && previousKey === `${sessionId}:false` };
}

/** Nothing to pick from: the provider was not running yet, or offers no choice. */
export function isEmptyTurnOptionCatalog(catalog: TurnOptionCatalog<unknown[], unknown[]>): boolean {
	return !catalog.models.length && !catalog.configOptions.length;
}
