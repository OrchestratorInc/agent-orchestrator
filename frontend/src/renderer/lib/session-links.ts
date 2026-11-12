import { artifactAtPath } from "../../../../packages/shared/artifacts/deliverables";
import { decodePath } from "./workspace-file-path";

export type SessionLinkTarget = { projectId: string; sessionId: string };
export type ArtifactLinkTarget = SessionLinkTarget & { path: string };
export type ResolvedSessionLink = SessionLinkTarget & { isTerminated: boolean };
export type SessionLinkWorkspace = {
	id: string;
	sessions: Array<{ id: string; isTerminated?: boolean; status?: string }>;
};

const SESSION_LINK_PREFIX = "ao://sessions/";
const ARTIFACT_SEGMENT = "artifacts";
const RAW_SESSION_LINK = /ao:\/\/sessions\/\S+/g;
const TRAILING_PUNCTUATION = /[.,;:!?]+$/;
const TRAILING_DELIMITERS = [
	["(", ")"],
	["[", "]"],
	["{", "}"],
	["<", ">"],
	["\"", "\""],
	["'", "'"],
	["`", "`"],
	["“", "”"],
	["‘", "’"],
	["«", "»"],
	["‹", "›"],
] as const;

/** Strictly parse the in-app session route, `ao://sessions/{project}/{session}`. */
export function parseSessionLink(value: string): SessionLinkTarget | undefined {
	const segments = linkSegments(value);
	if (segments?.length !== 2) return undefined;
	return sessionTarget(segments);
}

/** Strictly parse the in-app artifact route, `ao://sessions/{project}/{session}/artifacts/{path}`. */
export function parseArtifactLink(value: string): ArtifactLinkTarget | undefined {
	const segments = linkSegments(value);
	if (!segments || segments.length < 4 || segments[2] !== ARTIFACT_SEGMENT) return undefined;
	const target = sessionTarget(segments.slice(0, 2));
	if (!target) return undefined;
	const path: string[] = [];
	for (const segment of segments.slice(3)) {
		const decoded = decodeSegment(segment);
		if (!decoded?.trim() || decoded === "." || decoded === ".." || /[/\\]/.test(decoded)) return undefined;
		path.push(decoded);
	}
	return { ...target, path: path.join("/") };
}

/** The canonical artifact route for a slash-separated path inside a session's artifact directory. */
export function artifactLink(projectId: string, sessionId: string, path: string): string {
	const encodedPath = path.split("/").map(encodeURIComponent).join("/");
	return `${SESSION_LINK_PREFIX}${encodeURIComponent(projectId)}/${encodeURIComponent(sessionId)}/${ARTIFACT_SEGMENT}/${encodedPath}`;
}

type ArtifactPathWorkspace = { id: string; sessions: Array<{ id: string; artifactFiles?: Array<{ path: string; previousPaths?: string[] }> }> };

const ARTIFACT_DIRECTORY_MARKER = `/${ARTIFACT_SEGMENT}/`;

/**
 * The forms a link target can name an artifact by: as written, then with its
 * percent escapes decoded (Markdown encodes a link's target, a space as %20,
 * while a session lists the name as written). Each drops the link's query or
 * fragment (`?raw`, `#L10`) before decoding, so a name's own "#" or "?"
 * (%23, %3F) stays, and uses forward slashes, so a Windows path matches too.
 */
function linkTargetForms(value: string): string[] {
	const written = value.trim().split(/[?#]/, 1)[0]!;
	return [...new Set([written, decodePath(written)].map((form) => form.replace(/\\/g, "/")))];
}

/** True when a link target has an artifact directory segment, `.../artifacts/...`. */
export function hasArtifactDirectorySegment(value: string): boolean {
	return linkTargetForms(value).some((form) => form.includes(ARTIFACT_DIRECTORY_MARKER));
}

/**
 * The artifact route for a link target naming a listed session artifact, as
 * `{session}/{path}` or as a path containing `/artifacts/{session}/{path}`,
 * read as a link: without its query or fragment, as written or decoded.
 */
export function artifactLinkForTarget(value: string, workspaces: ArtifactPathWorkspace[]): string | undefined {
	for (const form of linkTargetForms(value)) {
		const link = listedArtifactLink(form, workspaces);
		if (link) return link;
	}
	return undefined;
}

/**
 * The artifact route for text naming a listed session artifact (inline code,
 * a worker report's artifact line), either as `{session}/{path}` or as a path
 * containing `/artifacts/{session}/{path}`. The text is a file path, where
 * "#" and "?" are part of the name, so it is matched as written first; then as
 * a link target, for a line or query written after it.
 */
export function artifactLinkForPath(value: string, workspaces: ArtifactPathWorkspace[]): string | undefined {
	const normalized = value.trim().replace(/\\/g, "/");
	const listed = listedArtifactLink(normalized, workspaces);
	if (listed) return listed;
	// Read as a link only when what follows "#" or "?" is a reference to a
	// place in the file: an unlisted `plan.md#draft` is its own file, never
	// the listed plan.md.
	const suffix = /[?#].*$/.exec(normalized)?.[0];
	return suffix === undefined || FILE_REFERENCE_SUFFIX.test(suffix) ? artifactLinkForTarget(value, workspaces) : undefined;
}

/** A line reference (`#L10`, `#L3-L7`, `#L2C4`) or `?raw` after a file path. */
const FILE_REFERENCE_SUFFIX = /^(?:#L\d+(?:C\d+)?(?:-L?\d+(?:C\d+)?)?|\?raw)$/i;

function listedArtifactLink(normalized: string, workspaces: ArtifactPathWorkspace[]): string | undefined {
	const candidates = normalized.startsWith("/") || /^[A-Za-z]:\//.test(normalized) ? [] : [normalized];
	for (let index = normalized.indexOf(ARTIFACT_DIRECTORY_MARKER); index >= 0; index = normalized.indexOf(ARTIFACT_DIRECTORY_MARKER, index + 1)) {
		candidates.push(normalized.slice(index + ARTIFACT_DIRECTORY_MARKER.length));
	}
	for (const candidate of candidates) {
		const slash = candidate.indexOf("/");
		if (slash <= 0) continue;
		const sessionId = candidate.slice(0, slash);
		const path = candidate.slice(slash + 1);
		for (const workspace of workspaces) {
			const session = workspace.sessions.find((entry) => entry.id === sessionId);
			const file = artifactAtPath(session?.artifactFiles, path);
			if (file) return artifactLink(workspace.id, sessionId, file.path);
		}
	}
	return undefined;
}

/**
 * A file path's place in one session's artifact directory (the worker writes
 * a session's artifacts under `.../artifacts/{session}/`). Undefined for any
 * other path, and for one that steps out of the directory. The path is a file
 * path, not a link: "#" and "?" are part of the name, so a link's query or
 * fragment is cut before its escapes are decoded, by whoever reads the link.
 */
export function sessionArtifactPath(path: string, sessionId: string): string | undefined {
	const normalized = path.trim().replace(/\\/g, "/");
	const marker = `${ARTIFACT_DIRECTORY_MARKER}${sessionId}/`;
	const index = normalized.indexOf(marker);
	if (!sessionId || index < 0) return undefined;
	const artifact = normalized.slice(index + marker.length);
	return artifact.split("/").every((segment) => segment !== "" && segment !== "." && segment !== "..") ? artifact : undefined;
}

/** True for either in-app route: a session or one of its artifacts. */
export function isAoLink(value: string): boolean {
	return parseSessionLink(value) !== undefined || parseArtifactLink(value) !== undefined;
}

function linkSegments(value: string): string[] | undefined {
	if (!value.startsWith(SESSION_LINK_PREFIX) || value.includes("?") || value.includes("#")) return undefined;
	const segments = value.slice(SESSION_LINK_PREFIX.length).split("/");
	return segments.some((segment) => segment.length === 0) ? undefined : segments;
}

function sessionTarget([project, session]: string[]): SessionLinkTarget | undefined {
	const projectId = decodeSegment(project!);
	const sessionId = decodeSegment(session!);
	if (!projectId?.trim() || !sessionId?.trim() || projectId.includes("/") || sessionId.includes("/")) return undefined;
	return { projectId, sessionId };
}

function decodeSegment(segment: string): string | undefined {
	try {
		return decodeURIComponent(segment);
	} catch {
		return undefined;
	}
}

export function isSessionLink(value: string): boolean {
	return parseSessionLink(value) !== undefined;
}

export function resolveSessionLink(value: string, workspaces: SessionLinkWorkspace[]): ResolvedSessionLink | undefined {
	const target = parseSessionLink(value) ?? parseArtifactLink(value);
	if (!target) return undefined;
	// Remote hosts merge into one list and each contributes its own standalone
	// entry, so several workspaces can share an id; the first id match is not
	// necessarily the one holding the session.
	for (const workspace of workspaces) {
		if (workspace.id !== target.projectId) continue;
		const session = workspace.sessions.find((candidate) => candidate.id === target.sessionId);
		if (session) return { projectId: target.projectId, sessionId: target.sessionId, isTerminated: session.isTerminated === true || session.status === "terminated" };
	}
	return undefined;
}

/** Find raw session and artifact links without swallowing prose punctuation adjacent to them. */
export function findSessionLinks(text: string): Array<{ start: number; end: number; text: string }> {
	const matches: Array<{ start: number; end: number; text: string }> = [];
	for (const match of text.matchAll(RAW_SESSION_LINK)) {
		const candidate = trimSessionLinkCandidate(match[0], text.slice(0, match.index));
		if (!isAoLink(candidate)) continue;
		matches.push({ start: match.index, end: match.index + candidate.length, text: candidate });
	}
	return matches;
}

function trimSessionLinkCandidate(value: string, precedingText: string): string {
	let candidate = value;
	let previous = "";
	while (candidate !== previous) {
		previous = candidate;
		if (precedingText.endsWith("&lt;") && candidate.endsWith("&gt;")) {
			candidate = candidate.slice(0, -"&gt;".length);
		}
		candidate = candidate.replace(TRAILING_PUNCTUATION, "");
		for (const [opening, closing] of TRAILING_DELIMITERS) {
			if (!candidate.endsWith(closing)) continue;
			const wrapsLink = precedingText.endsWith(opening);
			const unbalanced = opening !== closing && count(candidate, opening) < count(candidate, closing);
			if (wrapsLink || unbalanced) candidate = candidate.slice(0, -closing.length);
		}
	}
	return candidate;
}

function count(value: string, character: string): number {
	return [...value].filter((entry) => entry === character).length;
}

type MarkdownNode = { type: string; value?: string; url?: string; children?: MarkdownNode[] };
type TextMatch = { start: number; end: number; node: MarkdownNode };

const linkNode = (url: string, text: string): MarkdownNode => ({ type: "link", url, children: [{ type: "text", value: text }] });

/** remark plugin that linkifies only raw canonical session and artifact URLs outside code. */
export function remarkSessionLinks() {
	return (tree: MarkdownNode) =>
		visitMarkdown(tree, (text) => findSessionLinks(text).map((link) => ({ ...link, node: linkNode(link.text, link.text) })));
}

// Quotes and brackets around a word, and the prose punctuation after it.
const LEADING_WRAP = /^[("'“‘[<]+/;
const TRAILING_WRAP = /[)"'”’\]>.,;:!?]+$/;
// `~/x`, `./x`, `/a/b`; a bare `/word` reads as a slash command, not a path.
const ROOTED_PATH = /^(?:~|\.{1,2})\/\S|^\/[^\s/]+\//;
// `a/b/c`, or `a/b.ext` (`a/b.test.tsx`, `a/.env.local`), optionally with `:line[:col]`. A single `and/or` is prose.
const RELATIVE_PATH = /^[\w.-]+(?:\/[\w.@+-]+){2,}(?::\d+){0,2}$|^[\w.-]+\/[\w@+-]*(?:\.[\w@+-]+)+(?::\d+){0,2}$/;

/**
 * Plain-text references in prose nobody formatted: file paths become inline code,
 * and words that are ids of `sessionProjects` become links to those sessions.
 */
export function findPlainReferences(text: string, sessionProjects: ReadonlyMap<string, string>): TextMatch[] {
	const matches: TextMatch[] = [];
	for (const word of text.matchAll(/\S+/g)) {
		const lead = LEADING_WRAP.exec(word[0])?.[0].length ?? 0;
		const token = word[0].slice(lead).replace(TRAILING_WRAP, "");
		if (!token) continue;
		const projectId = sessionProjects.get(token);
		const node = projectId
			? linkNode(`${SESSION_LINK_PREFIX}${encodeURIComponent(projectId)}/${encodeURIComponent(token)}`, token)
			: ROOTED_PATH.test(token) || RELATIVE_PATH.test(token)
				? { type: "inlineCode", value: token }
				: undefined;
		const start = word.index + lead;
		if (node) matches.push({ start, end: start + token.length, node });
	}
	return matches;
}

/** remark plugin for `findPlainReferences`, outside code and links. */
export function remarkPlainReferences(sessionProjects: ReadonlyMap<string, string>) {
	return (tree: MarkdownNode) => visitMarkdown(tree, (text) => findPlainReferences(text, sessionProjects));
}

function visitMarkdown(node: MarkdownNode, find: (text: string) => TextMatch[]): void {
	// An authored link, inline or by reference, keeps its own target; a link inside it would fire both.
	if (!node.children || node.type === "code" || node.type === "inlineCode" || node.type === "link" || node.type === "linkReference") return;
	for (let index = 0; index < node.children.length; index += 1) {
		const child = node.children[index]!;
		if (child.type !== "text" || !child.value) {
			visitMarkdown(child, find);
			continue;
		}
		const matches = find(child.value);
		if (matches.length === 0) continue;
		const replacement: MarkdownNode[] = [];
		let cursor = 0;
		for (const match of matches) {
			if (match.start > cursor) replacement.push({ type: "text", value: child.value.slice(cursor, match.start) });
			replacement.push(match.node);
			cursor = match.end;
		}
		if (cursor < child.value.length) replacement.push({ type: "text", value: child.value.slice(cursor) });
		node.children.splice(index, 1, ...replacement);
		index += replacement.length - 1;
	}
}
