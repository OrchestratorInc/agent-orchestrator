// The name a file had when it was attached, by the generated path it was staged
// under. A display hint only: the message stores just the path, so a file this
// renderer did not attach shows that path's name instead.
const STORAGE_KEY = "ao.chatAttachmentNames.v1";
const MAX_FILES = 500;

// Every message with attachments reads this on render, so the parse is kept
// until the stored text changes.
let cached: { raw: string | null; names: Record<string, string> } | undefined;

function parse(raw: string | null): Record<string, string> {
	const names: Record<string, string> = {};
	try {
		const entries = JSON.parse(raw ?? "{}") as unknown;
		if (!entries || typeof entries !== "object" || Array.isArray(entries)) return names;
		// Only text is kept: anything else in storage would reach React as a child.
		for (const [path, name] of Object.entries(entries)) if (typeof name === "string") names[path] = name;
	} catch {
		// Unreadable storage is no names, not an error.
	}
	return names;
}

/** The remembered names, by staged path. Read-only: it is shared between callers. */
export function readAttachmentNames(): Readonly<Record<string, string>> {
	let raw: string | null = null;
	try {
		raw = window.localStorage.getItem(STORAGE_KEY);
	} catch {
		// Blocked storage reads as empty.
	}
	if (cached?.raw !== raw) cached = { raw, names: parse(raw) };
	return cached.names;
}

export function rememberAttachmentName(path: string, name: string): void {
	try {
		const entries = { ...readAttachmentNames() };
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
