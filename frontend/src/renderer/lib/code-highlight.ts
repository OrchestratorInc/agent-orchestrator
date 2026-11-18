/**
 * Shared syntax highlighting for chat, Markdown previews and file diffs.
 * Shiki uses the JavaScript regex engine with bundled grammars and AO's CSS
 * palette. Settled trees use a bounded LRU; streamed fences highlight only
 * newly completed lines, keeping their grammar state.
 */

import type { ElementContent, Root, RootContent } from "hast";
import type { CodeHighlighterPlugin, CodeHighlighterSession, CodeHighlightUpdate, CodeOptions } from "@pierre/words/html";
import type { CodeEngine } from "./code-highlight-engine";
import type { HighlightWorkerRequest, HighlightWorkerResponse } from "./code-highlight-protocol";

/**
 * Fence label → grammar. Labels are what agents type, so the aliases matter
 * more than the canonical names: `sh`, `console` and `zsh` all arrive in
 * practice and all mean bash here.
 *
 * A label that is absent is highlighted by nobody: `canonicalLanguage` returns
 * undefined, the block renders as plain monospace, and the grammar chunk is not
 * even fetched. That is the intended outcome for `text`, `log`, `output` and
 * anything this build does not know.
 */
const GRAMMARS = {
	bash: "bash",
	sh: "bash",
	shell: "bash",
	shellsession: "bash",
	console: "bash",
	zsh: "bash",
	css: "css",
	diff: "diff",
	patch: "diff",
	udiff: "diff",
	go: "go",
	golang: "go",
	html: "html",
	xml: "xml",
	svg: "xml",
	javascript: "javascript",
	js: "javascript",
	jsx: "jsx",
	mjs: "javascript",
	cjs: "javascript",
	json: "json",
	jsonc: "jsonc",
	json5: "json",
	markdown: "markdown",
	md: "markdown",
	python: "python",
	py: "python",
	rust: "rust",
	rs: "rust",
	sql: "sql",
	typescript: "typescript",
	ts: "typescript",
	tsx: "tsx",
	mts: "typescript",
	cts: "typescript",
	yaml: "yaml",
	yml: "yaml",
} as const;

export type GrammarName = (typeof GRAMMARS)[keyof typeof GRAMMARS];

/** The grammar for a fence label, or undefined when there is nothing to load. */
const grammarAliases = new Map<string, GrammarName>(Object.entries(GRAMMARS));

export function canonicalLanguage(label: string | undefined): GrammarName | undefined {
	return label ? grammarAliases.get(label.toLowerCase()) : undefined;
}

/* -------------------------------------------------------------------------- */
/* cache                                                                      */
/* -------------------------------------------------------------------------- */

/**
 * Bounded because a long session can scroll past thousands of blocks and this
 * holds parse trees, not strings. The caps are in source characters rather than
 * bytes: it is the only cost that can be measured for free, and a tree is a
 * near-constant multiple of the code it came from.
 */
const MAX_ENTRIES = 200;
const MAX_CHARS = 1_000_000;

/**
 * Above this, tokenizing moves off the render path even when the grammars are
 * already in memory. A pasted 2000-line file is the case that would otherwise
 * drop a frame; everything a chat block normally holds is far below it.
 */
const SYNC_LIMIT = 20_000;

/** Insertion order is the LRU order: a hit is re-inserted at the back. */
const cache = new Map<string, { tree: Root; chars: number }>();
let cachedChars = 0;

let engine: CodeEngine | undefined;
let loading: Promise<CodeEngine | undefined> | undefined;

/**
 * The highlighted tree, if it can be produced without waiting.
 *
 * Returns a cached tree, or tokenizes on the spot once the grammars are loaded
 * and the block is small. Both paths matter: the cache is what makes a
 * remounted block appear already highlighted, and the warm path is what keeps
 * every block after the first from flashing plain for a frame.
 */
export function highlightSync(code: string, language: GrammarName): Root | undefined {
	const key = cacheKey(code, language);
	const hit = cache.get(key);
	if (hit) {
		cache.delete(key);
		cache.set(key, hit);
		return hit.tree;
	}
	if (engine && code.length <= SYNC_LIMIT) return tokenize(code, language, key);
	return undefined;
}

/**
 * The highlighted tree, loading the grammars first if this is the first fence in
 * the session. Resolves undefined when highlighting is simply not available, so
 * callers keep showing plain monospace instead of showing nothing.
 */
export async function highlight(code: string, language: GrammarName): Promise<Root | undefined> {
	const ready = highlightSync(code, language);
	if (ready) return ready;
	if (code.length > SYNC_LIMIT) return highlightInWorker(code, language);
	if (!(await loadEngine())) return undefined;
	return tokenize(code, language, cacheKey(code, language));
}

async function loadEngine(): Promise<CodeEngine | undefined> {
	if (engine) return engine;
	loading ??= import("./code-highlight-engine")
		.then(async (module) => {
			engine = await module.createEngine();
			return engine;
		})
		.catch((cause: unknown) => {
			// A chunk that will not load in a packaged app will not load on the next
			// render either, so the failure is reported once and the rejected promise
			// is kept: plain monospace is a fine outcome and retrying is not.
			console.warn("chat: syntax grammars unavailable", cause);
			return undefined;
		});
	return loading;
}

function tokenize(code: string, language: GrammarName, key: string): Root | undefined {
	try {
		const tree = engine!.highlight(language, code);
		store(key, tree, code.length);
		return tree;
	} catch (cause) {
		// A grammar can throw on input it cannot make sense of. The block is still
		// readable unhighlighted, which is better than losing the message.
		console.warn(`chat: could not highlight ${language}`, cause);
		return undefined;
	}
}

function store(key: string, tree: Root, chars: number): void {
	const existing = cache.get(key);
	if (existing) {
		cachedChars -= existing.chars;
		cache.delete(key);
	}
	// One pathological block must not be able to evict everything else.
	if (chars > MAX_CHARS) return;
	while (cache.size >= MAX_ENTRIES || cachedChars + chars > MAX_CHARS) {
		const oldest = cache.keys().next().value;
		if (oldest === undefined) break;
		cachedChars -= cache.get(oldest)?.chars ?? 0;
		cache.delete(oldest);
	}
	cache.set(key, { tree, chars });
	cachedChars += chars;
}

/**
 * FNV-1a over the source, with the length alongside it.
 *
 * The code is not used as the key directly because that would retain a second
 * copy of every block ever highlighted. A 32-bit hash collides eventually, so
 * the length is carried as a cheap guard — two different blocks then have to
 * agree on hash *and* length to be confused.
 */
function cacheKey(code: string, language: GrammarName): string {
	let hash = 0x811c9dc5;
	for (let i = 0; i < code.length; i += 1) {
		hash ^= code.charCodeAt(i);
		hash = Math.imul(hash, 0x01000193);
	}
	return `${language}:${code.length}:${(hash >>> 0).toString(36)}`;
}

// Keep large tokenization off the renderer thread. If a worker is unavailable,
// saturated or stuck, leave the already-readable plain code in place.
const MAX_PENDING = 32;
const WORKER_TIMEOUT_MS = 10_000;
let worker: Worker | undefined;
let workerUnavailable = false;
let nextRequestId = 0;
let pendingChars = 0;
const pending = new Map<number, {
	key?: string;
	chars: number;
	promise: Promise<HighlightWorkerResponse | undefined>;
	resolve: (response: HighlightWorkerResponse | undefined) => void;
	timer: ReturnType<typeof setTimeout>;
}>();

function stopWorker(): void {
	workerUnavailable = true;
	worker?.terminate();
	worker = undefined;
	for (const request of pending.values()) {
		clearTimeout(request.timer);
		request.resolve(undefined);
	}
	pending.clear();
	pendingChars = 0;
}

async function highlightInWorker(code: string, language: GrammarName): Promise<Root | undefined> {
	const response = await sendWorker({ kind: "highlight", id: ++nextRequestId, code, language }, cacheKey(code, language));
	return response?.kind === "highlight" ? response.tree : undefined;
}

function sendWorker(request: HighlightWorkerRequest, key?: string): Promise<HighlightWorkerResponse | undefined> {
	if (key) {
		for (const entry of pending.values()) if (entry.key === key) return entry.promise;
	}
	if (workerUnavailable || typeof Worker === "undefined" || pending.size >= MAX_PENDING || pendingChars + request.code.length > MAX_CHARS) {
		return Promise.resolve(undefined);
	}
	try {
		if (!worker) {
			worker = new Worker(new URL("./code-highlight.worker.ts", import.meta.url), { type: "module" });
			worker.onerror = stopWorker;
			worker.onmessageerror = stopWorker;
			worker.onmessage = (event: MessageEvent<HighlightWorkerResponse>) => {
				const entry = pending.get(event.data.id);
				if (!entry) return;
				clearTimeout(entry.timer);
				pending.delete(event.data.id);
				pendingChars -= entry.chars;
				if (event.data.kind === "highlight" && event.data.tree && entry.key) store(entry.key, event.data.tree, entry.chars);
				entry.resolve(event.data);
			};
		}
		let resolve!: (response: HighlightWorkerResponse | undefined) => void;
		const promise = new Promise<HighlightWorkerResponse | undefined>((done) => { resolve = done; });
		pending.set(request.id, { key, chars: request.code.length, promise, resolve, timer: setTimeout(stopWorker, WORKER_TIMEOUT_MS) });
		pendingChars += request.code.length;
		worker.postMessage(request);
		return promise;
	} catch {
		stopWorker();
		return Promise.resolve(undefined);
	}
}

function tokensHtml(nodes: readonly RootContent[]): string {
	let html = "";
	for (const node of nodes) {
		if (node.type === "text") html += node.value.replace(/[&<>]/g, (char) => char === "&" ? "&amp;" : char === "<" ? "&lt;" : "&gt;");
		else if (node.type === "element") {
			const className = node.properties.className;
			const inner = tokensHtml(node.children);
			html += Array.isArray(className) ? `<span class="${className.join(" ")}">${inner}</span>` : inner;
		}
	}
	return html;
}

type LiveSession = CodeHighlighterSession & { accepts(options: CodeOptions): boolean };
const liveSessions = new Set<LiveSession>();

function createLiveSession(): LiveSession {
	let language: GrammarName | undefined;
	let source = "";
	let lines: string[] = [];
	let line: ((text: string, commit: boolean) => ElementContent[]) | undefined;
	const highlightLines = ({ code, language: label }: CodeOptions): CodeHighlightUpdate | null => {
		const grammar = canonicalLanguage(label);
		if (!grammar || !engine) {
			if (grammar) void loadEngine();
			return null;
		}
		code = code.replace(/\r\n?/g, "\n");
		if (grammar !== language || !line || !code.startsWith(source)) {
			language = grammar;
			source = "";
			lines = [];
			line = engine.createLines(grammar);
		}
		const previous = lines.length;
		const parts = code.split("\n");
		const tail = parts.pop()!;
		for (let at = lines.length; at < parts.length; at++) lines.push(tokensHtml(line(parts[at], true)));
		source = code.slice(0, code.length - tail.length);
		const lastContent = tail || parts.length === 0 ? parts.length : parts.length - 1;
		const first = Math.min(previous, lastContent);
		const changed = lines.slice(first);
		const tailHtml = tail ? tokensHtml(line(tail, false)) : "";
		changed.push(tailHtml);
		let lastLineOffset = 0;
		for (let at = 0; at < lastContent - first; at++) lastLineOffset += changed[at].length + 1;
		const committed = lines;
		return {
			firstLine: first,
			lineCount: parts.length + 1,
			html: changed.join("\n"),
			lastLineOffset,
			get fullHtml() {
				return [...committed, tailHtml].join("\n");
			},
		};
	};
	const session: LiveSession = {
		accepts: ({ code, language: label }) => line !== undefined && source !== "" && canonicalLanguage(label) === language && code.replace(/\r\n?/g, "\n").startsWith(source),
		highlightLines,
		highlight: (options) => highlightLines(options)?.fullHtml ?? null,
		dispose() {
			liveSessions.delete(session);
			line = undefined;
			lines = [];
			source = "";
		},
	};
	liveSessions.add(session);
	return session;
}

export const streamingCodeHighlighter: CodeHighlighterPlugin = {
	highlight(options) {
		for (const session of liveSessions) if (session.accepts(options)) return session.highlight(options);
		const grammar = canonicalLanguage(options.language);
		if (!grammar) return null;
		const tree = highlightSync(options.code, grammar);
		if (!tree) void loadEngine();
		return tree ? tokensHtml(tree.children) : null;
	},
	createSession: createLiveSession,
};
