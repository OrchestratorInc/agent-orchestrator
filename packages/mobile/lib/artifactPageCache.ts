/**
 * Inlined artifact pages, shared by the gallery, the viewer and chat frames,
 * so a page shown again is read once per version of the file.
 */

const CACHE_SIZE = 12;
const cache = new Map<string, Promise<string>>();

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
	if (hit) return hit;
	const pending = build();
	cache.set(versioned, pending);
	// A failure is not kept: the next mount tries again.
	pending.catch(() => cache.delete(versioned));
	while (cache.size > CACHE_SIZE) cache.delete(cache.keys().next().value!);
	return pending;
}
