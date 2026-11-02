/**
 * Inlined artifact pages, shared by the gallery, the viewer and chat frames,
 * so a page shown again is read once per version of the file. An inlined page
 * carries its images as base64, so the cache is held to a total size, not
 * only to a number of pages.
 */

const MAX_PAGES = 12;
/** All kept pages together, in characters (a base64 page is one byte each). */
export const MAX_CACHED_CHARS = 24 << 20;

type Entry = { pending: Promise<string>; size: number };
const cache = new Map<string, Entry>();
let cachedChars = 0;

function drop(key: string): void {
	const entry = cache.get(key);
	if (!entry) return;
	cachedChars -= entry.size;
	cache.delete(key);
}

/**
 * The page for `key`, built once per `version` (the file's modified time).
 * Without a version a rewrite could not be told apart, so the page is read
 * afresh and not shared: a chat frame in an orchestrator's thread, whose files
 * the phone has no list of, and a file the session does not list.
 */
export function cachedArtifactPage(key: string, version: string | undefined, build: () => Promise<string>): Promise<string> {
	if (!version) return build();
	const versioned = `${key}|${version}`;
	const hit = cache.get(versioned);
	if (hit) return hit.pending;
	const entry: Entry = { pending: build(), size: 0 };
	cache.set(versioned, entry);
	while (cache.size > MAX_PAGES) drop(cache.keys().next().value!);
	entry.pending.then((html) => {
		if (cache.get(versioned) !== entry) return;
		entry.size = html.length;
		cachedChars += html.length;
		// Oldest first; a page larger than the whole budget is not kept at all.
		for (const old of cache.keys()) {
			if (cachedChars <= MAX_CACHED_CHARS) break;
			if (old !== versioned) drop(old);
		}
		if (cachedChars > MAX_CACHED_CHARS) drop(versioned);
	}, () => {
		// A failure is not kept: the next mount tries again.
		if (cache.get(versioned) === entry) drop(versioned);
	});
	return entry.pending;
}
