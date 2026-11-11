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

/**
 * A diagram's identity: its theme and its whole source. A source is at most
 * MAX_DIAGRAM_CHARS and the cache holds MAX_ENTRIES, so the full text costs
 * little, and no two diagrams can share an entry as two hashes could.
 */
export function diagramCacheKey(code: string, scheme: "light" | "dark"): string {
	return `${scheme}\n${code}`;
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
