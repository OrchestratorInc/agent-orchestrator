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

type ArtifactPathWorkspace = { id: string; sessions: Array<{ id: string; artifactFiles?: Array<{ path: string }> }> };

/**
 * The artifact route for text naming a listed session artifact, either as
 * `{session}/{path}` or as a path containing `/artifacts/{session}/{path}`.
 */
export function artifactLinkForPath(value: string, workspaces: ArtifactPathWorkspace[]): string | undefined {
	const normalized = value.trim().replace(/\\/g, "/");
	const candidates = normalized.startsWith("/") || /^[A-Za-z]:\//.test(normalized) ? [] : [normalized];
	const marker = `/${ARTIFACT_SEGMENT}/`;
	for (let index = normalized.indexOf(marker); index >= 0; index = normalized.indexOf(marker, index + 1)) {
		candidates.push(normalized.slice(index + marker.length));
	}
	for (const candidate of candidates) {
		const slash = candidate.indexOf("/");
		if (slash <= 0) continue;
		const sessionId = candidate.slice(0, slash);
		const path = candidate.slice(slash + 1);
		for (const workspace of workspaces) {
			const session = workspace.sessions.find((entry) => entry.id === sessionId);
			if (session?.artifactFiles?.some((file) => file.path === path)) return artifactLink(workspace.id, sessionId, path);
		}
	}
	return undefined;
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
	const project = workspaces.find((workspace) => workspace.id === target.projectId);
	const session = project?.sessions.find((candidate) => candidate.id === target.sessionId);
	if (!session) return undefined;
	return { projectId: target.projectId, sessionId: target.sessionId, isTerminated: session.isTerminated === true || session.status === "terminated" };
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

/** remark plugin that linkifies only raw canonical session and artifact URLs outside code. */
export function remarkSessionLinks() {
	return (tree: MarkdownNode) => visitMarkdown(tree);
}

function visitMarkdown(node: MarkdownNode): void {
	if (!node.children || node.type === "code" || node.type === "inlineCode" || node.type === "link") return;
	for (let index = 0; index < node.children.length; index += 1) {
		const child = node.children[index]!;
		if (child.type !== "text" || !child.value) {
			visitMarkdown(child);
			continue;
		}
		const links = findSessionLinks(child.value);
		if (links.length === 0) continue;
		const replacement: MarkdownNode[] = [];
		let cursor = 0;
		for (const link of links) {
			if (link.start > cursor) replacement.push({ type: "text", value: child.value.slice(cursor, link.start) });
			replacement.push({ type: "link", url: link.text, children: [{ type: "text", value: link.text }] });
			cursor = link.end;
		}
		if (cursor < child.value.length) replacement.push({ type: "text", value: child.value.slice(cursor) });
		node.children.splice(index, 1, ...replacement);
		index += replacement.length - 1;
	}
}
