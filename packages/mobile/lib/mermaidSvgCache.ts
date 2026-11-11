/**
 * Diagrams already drawn, by source and theme: a message or document that
 * scrolls back into view draws from here instead of loading the engine again.
 * Bounded like the desktop's diagram cache, since an SVG runs larger than its
 * source; the oldest entry goes first.
 */
const MAX_ENTRIES = 40;
const MAX_CHARS = 2_000_000;
const cache = new Map<string, string>();
let cachedChars = 0;

export function diagramCacheKey(code: string, scheme: "light" | "dark"): string {
	let hash = 0x811c9dc5;
	for (let i = 0; i < code.length; i += 1) {
		hash ^= code.charCodeAt(i);
		hash = Math.imul(hash, 0x01000193);
	}
	return `${scheme}:${code.length}:${(hash >>> 0).toString(36)}`;
}

export function cachedDiagram(key: string): string | undefined {
	return cache.get(key);
}

export function storeDiagram(key: string, svg: string): void {
	if (svg.length > MAX_CHARS || cache.has(key)) return;
	while (cache.size >= MAX_ENTRIES || cachedChars + svg.length > MAX_CHARS) {
		const oldest = cache.keys().next().value;
		if (oldest === undefined) break;
		cachedChars -= cache.get(oldest)?.length ?? 0;
		cache.delete(oldest);
	}
	cache.set(key, svg);
	cachedChars += svg.length;
}

/** Test-only reset. */
export function clearDiagramCache(): void {
	cache.clear();
	cachedChars = 0;
}
