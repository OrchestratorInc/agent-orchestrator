import { createContext, Fragment, useContext, useMemo, useSyncExternalStore, type ReactNode } from "react";
import type { ChatSkill } from "../../types/conversation";

/**
 * A skill or file chip the composer sent. The message itself carries only `wire`
 * (`/review`, `src/app.ts`, `"docs/a b.md"`), so a sent message can mark it again
 * only for a wire this records or a skill the session lists.
 */
export type ComposerSentToken = {
	kind: "skill" | "file";
	/** The skill name or the workspace path. */
	value: string;
	/** What the chip shows: `/review`, or a file's name. */
	display: string;
	/** The text the chip put in the message. */
	wire: string;
};

export type ComposerTokenSegment = { text: string; token?: ComposerSentToken };

const STORAGE_KEY = "ao:chat:sent-composer-tokens:v1";
/** Scopes kept, oldest dropped first. A scope is a session, or a first message sent before its session existed. */
const MAX_SCOPES = 100;
const MAX_TOKENS_PER_SCOPE = 100;
const MAX_WIRE_LENGTH = 1024;

type Store = Array<[scope: string, tokens: ComposerSentToken[]]>;

const EMPTY: readonly ComposerSentToken[] = [];
let cache: Store | undefined;
const listeners = new Set<() => void>();

function validToken(value: unknown): value is ComposerSentToken {
	if (!value || typeof value !== "object") return false;
	const token = value as Record<string, unknown>;
	return (
		(token.kind === "skill" || token.kind === "file") &&
		typeof token.value === "string" &&
		typeof token.display === "string" &&
		typeof token.wire === "string" &&
		token.wire.length > 0 &&
		token.wire.length <= MAX_WIRE_LENGTH
	);
}

function load(): Store {
	if (cache) return cache;
	try {
		const parsed: unknown = JSON.parse(globalThis.localStorage?.getItem(STORAGE_KEY) ?? "[]");
		cache = Array.isArray(parsed)
			? parsed.flatMap((entry): Store => {
					if (!Array.isArray(entry) || typeof entry[0] !== "string" || !Array.isArray(entry[1])) return [];
					return [[entry[0], entry[1].filter(validToken)]];
				})
			: [];
	} catch {
		cache = [];
	}
	return cache;
}

function notify() {
	for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	// Another window sending keeps this one's chips in step.
	const onStorage = (event: StorageEvent) => {
		if (event.key !== STORAGE_KEY) return;
		cache = undefined;
		listener();
	};
	globalThis.addEventListener?.("storage", onStorage);
	return () => {
		listeners.delete(listener);
		globalThis.removeEventListener?.("storage", onStorage);
	};
}

export function sessionTokenScope(sessionId: string): string {
	return `session:${sessionId}`;
}

/**
 * A send with no session yet (the new chat page) is found again by its text,
 * stored as a hash and length so a long prompt does not fill storage.
 */
export function messageTokenScope(text: string): string {
	const trimmed = text.trim();
	let hash = 5381;
	for (let index = 0; index < trimmed.length; index++) hash = ((hash * 33) ^ trimmed.charCodeAt(index)) >>> 0;
	return `message:${hash.toString(36)}:${trimmed.length}`;
}

/** Remembers the chips a send carried, so its bubble can show them again. */
export function recordSentComposerTokens(scope: string, tokens: readonly ComposerSentToken[]): void {
	const sent = tokens.filter(validToken);
	if (sent.length === 0) return;
	const store = load();
	const previous = store.find(([key]) => key === scope)?.[1] ?? [];
	const merged = [...previous.filter((token) => !sent.some((next) => next.wire === token.wire)), ...sent]
		.slice(-MAX_TOKENS_PER_SCOPE);
	cache = [...store.filter(([key]) => key !== scope), [scope, merged] as [string, ComposerSentToken[]]].slice(-MAX_SCOPES);
	try {
		globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(cache));
	} catch {
		// Storage full or unavailable: the chips still show until reload.
	}
	notify();
}

/** The recorded chips for these scopes, re-read when a send records more. */
function useSentComposerTokens(scopes: readonly string[]): readonly ComposerSentToken[] {
	const store = useSyncExternalStore(subscribe, load, load);
	const key = scopes.join("\n");
	return useMemo(() => {
		const found = key.split("\n").flatMap((scope) => store.find(([entry]) => entry === scope)?.[1] ?? []);
		return found.length > 0 ? found : EMPTY;
	}, [store, key]);
}

export function skillToken(skill: Pick<ChatSkill, "name">): ComposerSentToken {
	return { kind: "skill", value: skill.name, display: `/${skill.name}`, wire: `/${skill.name}` };
}

/** Code the user wrote, fenced or inline, is never turned into chips. */
const CODE_SPANS = /```[\s\S]*?(?:```|$)|`[^`\n]+`/g;

function escapeRegExp(text: string): string {
	return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Builds a splitter for text at known chip wires, compiled once per token set. A
 * wire counts only as a whole word: at the start or after whitespace, and followed
 * by the end, whitespace, or closing punctuation. So `/reviewer`, `a/review`,
 * `@review` and `/Users/me` stay text.
 */
export function composerTokenSplitter(tokens: readonly ComposerSentToken[]): (text: string) => ComposerTokenSegment[] {
	const byWire = new Map<string, ComposerSentToken>();
	for (const token of tokens) if (!byWire.has(token.wire)) byWire.set(token.wire, token);
	if (byWire.size === 0) return (text) => [{ text }];
	// Longest first, so a wire that extends another wins.
	const alternatives = [...byWire.keys()].sort((a, b) => b.length - a.length).map(escapeRegExp);
	const pattern = new RegExp(`(?<=^|\\s)(?:${alternatives.join("|")})(?=$|\\s|[.,;:!?)\\]]+(?:$|\\s))`, "g");
	return (text) => (text ? splitAt(text, pattern, byWire) : [{ text }]);
}

function splitAt(text: string, pattern: RegExp, byWire: ReadonlyMap<string, ComposerSentToken>): ComposerTokenSegment[] {
	const code = [...text.matchAll(CODE_SPANS)].map((match) => [match.index, match.index + match[0].length] as const);
	const segments: ComposerTokenSegment[] = [];
	let cursor = 0;
	for (const match of text.matchAll(pattern)) {
		const start = match.index;
		const end = start + match[0].length;
		if (code.some(([from, to]) => start < to && end > from)) continue;
		const token = byWire.get(match[0]);
		if (!token) continue;
		if (start > cursor) segments.push({ text: text.slice(cursor, start) });
		segments.push({ text: match[0], token });
		cursor = end;
	}
	if (segments.length === 0) return [{ text }];
	if (cursor < text.length) segments.push({ text: text.slice(cursor) });
	return segments;
}

/** The skills the session's agent lists, so a sent `/name` is marked like the chip it was. */
export const ChatTokenSkills = createContext<readonly ChatSkill[]>([]);

/** Renders a run of text, marking the sent skills and files in it. */
export type ComposerTokenRenderer = (text: string, renderPlain?: (text: string) => ReactNode) => ReactNode;

/**
 * A text renderer that colors a sent message's skills and files, or undefined when
 * the message can have none. Only exact wires count: a skill the session lists, or
 * a chip this app recorded when the message was sent. The text stays what was sent,
 * so selection, copy and find are unchanged. It handles one run of inline text, so
 * a Markdown renderer can call it per text node (list items and emphasis included)
 * and leave code elements alone. Stable while the known tokens are.
 */
export function useComposerTokenRenderer(sessionId: string, text: string): ComposerTokenRenderer | undefined {
	const skills = useContext(ChatTokenSkills);
	const textScope = useMemo(() => messageTokenScope(text), [text]);
	const recorded = useSentComposerTokens([sessionTokenScope(sessionId), textScope]);
	return useMemo(() => {
		const tokens = skills.length > 0 ? [...recorded, ...skills.map(skillToken)] : recorded;
		if (tokens.length === 0) return undefined;
		const split = composerTokenSplitter(tokens);
		return (run: string, renderPlain: (text: string) => ReactNode = (plain) => plain) => {
			const segments = split(run);
			if (segments.length === 1 && !segments[0]?.token) return renderPlain(run);
			return segments.map((segment, index) =>
				segment.token ? (
					<span key={index} data-message-token={segment.token.kind} className="text-logo-accent">
						{segment.text}
					</span>
				) : (
					<Fragment key={index}>{renderPlain(segment.text)}</Fragment>
				),
			);
		};
	}, [recorded, skills]);
}
