import { isEmptyTurnOptionCatalog } from "./turnOptionsCatalog";
import type { ChatConfigOption, ChatModel } from "./types";

export type TurnOptions = { models: ChatModel[]; configOptions: ChatConfigOption[] };

// The last catalog each chat offered, kept past the cache's 30 seconds. A chat
// opened while hibernated has no catalog until its provider is back, and showing
// the previous one keeps the model in the composer instead of dropping it.
// Twice the 30-second caches' 16: they only need chats opened in the last half
// minute, this needs every chat someone moves between, and entries are small.
const LAST_TURN_OPTIONS_LIMIT = 32;
const lastTurnOptions = new Map<string, TurnOptions>();

export function rememberTurnOptions(key: string, catalog: TurnOptions): void {
	if (!key || isEmptyTurnOptionCatalog(catalog)) return;
	lastTurnOptions.delete(key);
	lastTurnOptions.set(key, catalog);
	while (lastTurnOptions.size > LAST_TURN_OPTIONS_LIMIT) {
		const oldest = lastTurnOptions.keys().next().value;
		if (oldest === undefined) break;
		lastTurnOptions.delete(oldest);
	}
}

export function rememberedTurnOptions(key: string): TurnOptions | undefined {
	return lastTurnOptions.get(key);
}

/**
 * Drops every remembered catalog when a machine is forgotten. Keys name an
 * address and password rather than the machine, and a catalog is cheap to read
 * again, so all of them go.
 */
export function forgetRememberedTurnOptions(): void {
	lastTurnOptions.clear();
}
