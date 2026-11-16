// The model each chat's agent last reported running, so a chat that is still
// starting names it at once. A display hint only; the agent's report replaces it.
const STORAGE_KEY = "ao.chatLastModel.v1";
const MAX_CHATS = 500;

type Entries = Record<string, { harness: string; model: string }>;

function read(): Entries {
	try {
		const entries = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "{}") as unknown;
		return entries && typeof entries === "object" && !Array.isArray(entries) ? entries as Entries : {};
	} catch {
		return {};
	}
}

/** The model this chat last ran under this agent, if it was seen running. */
export function readChatLastModel(chatKey: string, harness: string | undefined): string | undefined {
	const entry = read()[chatKey];
	return harness && entry?.harness === harness && typeof entry.model === "string" ? entry.model : undefined;
}

export function rememberChatLastModel(chatKey: string, harness: string, model: string): void {
	try {
		const entries = read();
		if (entries[chatKey]?.harness === harness && entries[chatKey]?.model === model) return;
		delete entries[chatKey];
		entries[chatKey] = { harness, model };
		// Insertion order is recency, so the oldest chats drop first.
		const keys = Object.keys(entries);
		for (const key of keys.slice(0, Math.max(0, keys.length - MAX_CHATS))) delete entries[key];
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(entries));
	} catch {
		// Storage can be full or blocked; the chat just names its model a moment later.
	}
}
