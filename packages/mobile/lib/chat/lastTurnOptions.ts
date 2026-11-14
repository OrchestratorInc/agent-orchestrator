import { isEmptyTurnOptionCatalog } from "./turnOptionsCatalog";
import type { ChatConfigOption, ChatModel } from "./types";

export type TurnOptions = { models: ChatModel[]; configOptions: ChatConfigOption[] };

// The last catalog each chat offered, kept past the cache's 30 seconds. A chat
// opened while hibernated has no catalog until its provider is back, and showing
// the previous one keeps the model in the composer instead of dropping it.
// Twice the 30-second caches' 16: they only need chats opened in the last half
// minute, this needs every chat someone moves between, and entries are small.
const LAST_TURN_OPTIONS_LIMIT = 32;
// Each with the agent that offered it: a chat can switch agents, and one
// agent's models must never stand in for another's.
const lastTurnOptions = new Map<string, { catalog: TurnOptions; harness?: string }>();

export function rememberTurnOptions(key: string, catalog: TurnOptions, harness?: string): void {
	if (!key || isEmptyTurnOptionCatalog(catalog)) return;
	lastTurnOptions.delete(key);
	lastTurnOptions.set(key, { catalog, harness: harness || undefined });
	while (lastTurnOptions.size > LAST_TURN_OPTIONS_LIMIT) {
		const oldest = lastTurnOptions.keys().next().value;
		if (oldest === undefined) break;
		lastTurnOptions.delete(oldest);
	}
}

/**
 * The chat's last catalog. Asked for a given agent, one another agent offered
 * is not returned; asked without one (the chat's agent not known yet), the
 * last catalog is.
 */
export function rememberedTurnOptions(key: string, harness?: string): TurnOptions | undefined {
	const remembered = lastTurnOptions.get(key);
	if (!remembered) return undefined;
	if (harness && remembered.harness && remembered.harness !== harness) return undefined;
	return remembered.catalog;
}

/**
 * Drops every remembered catalog when a machine is forgotten. Keys name an
 * address and password rather than the machine, and a catalog is cheap to read
 * again, so all of them go.
 */
export function forgetRememberedTurnOptions(): void {
	lastTurnOptions.clear();
}
