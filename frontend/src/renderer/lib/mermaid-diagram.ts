import { isRenderableDiagram, MAX_DIAGRAM_CHARS } from "../../../../packages/shared/files/file-formats";

export type DiagramTheme = "light" | "dark";

export { isRenderableDiagram, MAX_DIAGRAM_CHARS };

const MAX_ENTRIES = 50;
const MAX_CHARS = 500_000;
const cache = new Map<string, string>();
let cachedChars = 0;

function cacheKey(code: string, theme: DiagramTheme): string {
	let hash = 0x811c9dc5;
	for (let i = 0; i < code.length; i += 1) {
		hash ^= code.charCodeAt(i);
		hash = Math.imul(hash, 0x01000193);
	}
	return `${theme}:${code.length}:${(hash >>> 0).toString(36)}`;
}

function store(key: string, svg: string): void {
	if (svg.length > MAX_CHARS) return;
	while (cache.size >= MAX_ENTRIES || cachedChars + svg.length > MAX_CHARS) {
		const oldest = cache.keys().next().value;
		if (oldest === undefined) break;
		cachedChars -= cache.get(oldest)?.length ?? 0;
		cache.delete(oldest);
	}
	cache.set(key, svg);
	cachedChars += svg.length;
}

export function clearDiagramCache(): void {
	cache.clear();
	cachedChars = 0;
}

type MermaidModule = typeof import("mermaid").default;

let mermaidPromise: Promise<MermaidModule> | undefined;
let initializedTheme: DiagramTheme | undefined;
let renderCounter = 0;

function loadMermaid(): Promise<MermaidModule> {
	mermaidPromise ??= import("mermaid").then((module) => module.default as unknown as MermaidModule);
	return mermaidPromise;
}

export async function renderMermaidDiagram(code: string, theme: DiagramTheme): Promise<string> {
	if (!isRenderableDiagram(code)) {
		throw new Error(code.trim().length === 0 ? "empty mermaid block" : "mermaid block too large");
	}
	const key = cacheKey(code, theme);
	const hit = cache.get(key);
	if (hit) return hit;

	const mermaid = await loadMermaid();
	if (initializedTheme !== theme) {
		mermaid.initialize({
			startOnLoad: false,
			theme: theme === "dark" ? "dark" : "default",
			securityLevel: "strict",
			suppressErrorRendering: true,
		});
		initializedTheme = theme;
	}

	await mermaid.parse(code);

	renderCounter += 1;
	const id = `ao-mermaid-${renderCounter.toString(36)}-${Date.now().toString(36)}`;
	const { svg } = await mermaid.render(id, code);

	const { default: DOMPurify } = await import("dompurify");
	const clean = DOMPurify.sanitize(svg, {
		USE_PROFILES: { svg: true, html: true },
		ADD_TAGS: ["foreignObject"],
		HTML_INTEGRATION_POINTS: { foreignobject: true },
		FORBID_TAGS: [
			"script",
			"iframe",
			"object",
			"embed",
			"link",
			"meta",
			"img",
			"image",
			"video",
			"audio",
			"source",
			"track",
			"input",
			"button",
			"form",
			"textarea",
			"select",
		],
	});
	if (!clean || !clean.includes("<svg")) {
		throw new Error("mermaid produced no diagram");
	}
	store(key, clean);
	return clean;
}
