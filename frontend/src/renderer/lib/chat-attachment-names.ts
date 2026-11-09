// The name a file had when it was attached, by the generated path it was staged
// under. A display hint only: the message stores just the path, so a file this
// renderer did not attach shows that path's name instead.
const STORAGE_KEY = "ao.chatAttachmentNames.v1";
const MAX_FILES = 500;

export function readAttachmentNames(): Record<string, string> {
	try {
		const entries = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "{}") as unknown;
		return entries && typeof entries === "object" && !Array.isArray(entries) ? entries as Record<string, string> : {};
	} catch {
		return {};
	}
}

export function rememberAttachmentName(path: string, name: string): void {
	try {
		const entries = readAttachmentNames();
		delete entries[path];
		entries[path] = name;
		// Insertion order is recency, so the oldest files drop first.
		const keys = Object.keys(entries);
		for (const key of keys.slice(0, Math.max(0, keys.length - MAX_FILES))) delete entries[key];
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(entries));
	} catch {
		// Storage can be full or blocked; the file just shows its generated name.
	}
}
