const STORAGE_KEY = "ao.chatAttachmentNames.v1";
const MAX_FILES = 500;

let cached: { raw: string | null; names: Record<string, string> } | undefined;

function parse(raw: string | null): Record<string, string> {
	const names: Record<string, string> = {};
	try {
		const entries = JSON.parse(raw ?? "{}") as unknown;
		if (!entries || typeof entries !== "object" || Array.isArray(entries)) return names;
		for (const [path, name] of Object.entries(entries)) if (typeof name === "string") names[path] = name;
	} catch {
	}
	return names;
}

export function readAttachmentNames(): Readonly<Record<string, string>> {
	let raw: string | null = null;
	try {
		raw = window.localStorage.getItem(STORAGE_KEY);
	} catch {
	}
	if (cached?.raw !== raw) cached = { raw, names: parse(raw) };
	return cached.names;
}

export function rememberAttachmentName(path: string, name: string): void {
	try {
		const entries = { ...readAttachmentNames() };
		delete entries[path];
		entries[path] = name;
		const keys = Object.keys(entries);
		for (const key of keys.slice(0, Math.max(0, keys.length - MAX_FILES))) delete entries[key];
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(entries));
	} catch {
	}
}
