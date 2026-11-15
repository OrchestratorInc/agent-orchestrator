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
		const keys = Object.keys(entries);
		for (const key of keys.slice(0, Math.max(0, keys.length - MAX_CHATS))) delete entries[key];
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(entries));
	} catch {
	}
}
