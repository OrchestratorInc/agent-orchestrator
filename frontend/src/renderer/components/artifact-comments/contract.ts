/**
 * PROVISIONAL: the comments contract for shared artifact links, written to
 * the design (sections 5, 6 and 7) until packages/shared/artifact-comments
 * lands. Once it does, this file re-exports from there and is otherwise
 * deleted; nothing outside artifact-comments/ imports it directly except
 * through the names below.
 */

/** Where a thread points in one version of the page: a structural path plus checks. */
export type ArtifactCommentAnchor = {
	/** tag:index among same-tag element siblings, from <body>, e.g. "body>main:1>h1:1". */
	path: string;
	tag: string;
	/** sha256 of the element's first 200 normalised characters, 16 hex characters. */
	textHash: string;
	/** Up to 120 characters of the element's text: for the agent message, never shown in the UI. */
	excerpt?: string;
};

/** A comment's or resolver's author; null once the account is deleted. */
export type ArtifactCommentAuthor = { id: string; name: string };

export type ArtifactComment = {
	id: string;
	author: ArtifactCommentAuthor | null;
	body: string;
	createdAt: string;
	editedAt: string | null;
	deleted: boolean;
};

export type ArtifactCommentThreadStatus = "open" | "resolved";

export type ArtifactCommentThread = {
	id: string;
	version: number;
	status: ArtifactCommentThreadStatus;
	anchor: ArtifactCommentAnchor;
	createdAt: string;
	resolvedBy: ArtifactCommentAuthor | null;
	resolvedAt: string | null;
	sentToAgentAt: string | null;
	/** Oldest first; the first is the thread's opening comment. */
	comments: ArtifactComment[];
};

/** GET /artifact-shares/{slug}/comments: every thread of one version. */
export type ArtifactCommentsSnapshot = {
	version: number;
	currentVersion: number;
	revision: number;
	you: { userId: string; isOwner: boolean };
	threads: ArtifactCommentThread[];
};

export type CreateArtifactCommentThreadRequest = { version: number; anchor: ArtifactCommentAnchor; body: string; clientId: string };
export type ReplyArtifactCommentThreadRequest = { body: string; clientId: string };

/** The longest comment body the control plane takes, in characters. */
export const MAX_ARTIFACT_COMMENT_BODY = 5000;
/** The longest note the owner can add when sending a thread to the agent. */
export const MAX_ARTIFACT_COMMENT_NOTE = 2000;

// ---------------------------------------------------------------------------
// Element groups (section 7)

/** Where the bridge found a commented element, top to bottom in the page. */
export type ArtifactCommentPosition = { path: string; top: number; found: boolean };

/** The threads on one element, oldest first. */
export type ArtifactCommentGroup = { path: string; anchor: ArtifactCommentAnchor; threads: ArtifactCommentThread[] };

export type ArtifactCommentGroups = {
	/** Elements with at least one open thread, in document order; open threads only. */
	open: ArtifactCommentGroup[];
	/** Elements with resolved threads, in document order; resolved threads only. */
	resolved: ArtifactCommentGroup[];
	/** Threads whose element the bridge reported not found, oldest first. */
	unanchored: ArtifactCommentThread[];
};

function byCreated(a: { createdAt: string }, b: { createdAt: string }): number {
	return (Date.parse(a.createdAt) || 0) - (Date.parse(b.createdAt) || 0);
}

/**
 * Threads grouped by element. Groups follow the reported positions (document
 * order); an element not reported yet goes after the reported ones, by its
 * oldest thread. Threads within a group are oldest first.
 */
export function groupArtifactCommentThreads(
	threads: readonly ArtifactCommentThread[],
	positions: readonly ArtifactCommentPosition[] = [],
): ArtifactCommentGroups {
	const where = new Map(positions.map((position) => [position.path, position]));
	const unanchored: ArtifactCommentThread[] = [];
	const build = (status: ArtifactCommentThreadStatus): ArtifactCommentGroup[] => {
		const groups = new Map<string, ArtifactCommentGroup>();
		for (const thread of threads) {
			if (thread.status !== status) continue;
			if (where.get(thread.anchor.path)?.found === false) continue;
			const group = groups.get(thread.anchor.path) ?? { path: thread.anchor.path, anchor: thread.anchor, threads: [] };
			group.threads.push(thread);
			groups.set(thread.anchor.path, group);
		}
		for (const group of groups.values()) group.threads.sort(byCreated);
		return [...groups.values()].sort((a, b) => {
			const ta = where.get(a.path)?.top;
			const tb = where.get(b.path)?.top;
			if (ta !== undefined && tb !== undefined && ta !== tb) return ta - tb;
			if (ta !== undefined && tb === undefined) return -1;
			if (ta === undefined && tb !== undefined) return 1;
			return byCreated(a.threads[0], b.threads[0]);
		});
	};
	for (const thread of threads) {
		if (where.get(thread.anchor.path)?.found === false) unanchored.push(thread);
	}
	unanchored.sort(byCreated);
	return { open: build("open"), resolved: build("resolved"), unanchored };
}

/** How many open threads each element has, for the bridge's markers. */
export function artifactCommentMarkers(threads: readonly ArtifactCommentThread[]): { path: string; textHash: string; count: number }[] {
	const markers = new Map<string, { path: string; textHash: string; count: number }>();
	for (const thread of threads) {
		const marker = markers.get(thread.anchor.path) ?? { path: thread.anchor.path, textHash: thread.anchor.textHash, count: 0 };
		if (thread.status === "open") marker.count += 1;
		markers.set(thread.anchor.path, marker);
	}
	return [...markers.values()];
}

// ---------------------------------------------------------------------------
// Bridge protocol (section 6)

/** The one window message the viewer sends the frame: it carries the private port. */
export const ARTIFACT_COMMENTS_BRIDGE_CONNECT = "ao:comments-bridge:connect";
export const ARTIFACT_COMMENTS_BRIDGE_PROTOCOL = 1;
/** How long the viewer waits for ready before saying the page can't be pointed at. */
export const ARTIFACT_COMMENTS_BRIDGE_READY_TIMEOUT_MS = 3_000;

export type ArtifactCommentsBridgeKind = "markdown" | "html";

/** Viewer → bridge, over the port. */
export type ArtifactCommentsToBridge =
	| { type: "mode"; on: boolean; kind: ArtifactCommentsBridgeKind }
	| { type: "anchors"; anchors: { path: string; textHash: string; count: number }[] }
	| { type: "select"; path: string }
	| { type: "clear" }
	| { type: "theme"; accent: string; tint: string };

/** Bridge → viewer, over the port. */
export type ArtifactCommentsFromBridge =
	| { type: "ready" }
	| { type: "pick"; anchor: ArtifactCommentAnchor }
	| { type: "marker"; path: string }
	| { type: "positions"; positions: ArtifactCommentPosition[] }
	| { type: "escape" };

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null;
const isString = (value: unknown): value is string => typeof value === "string";

function parseAnchor(value: unknown): ArtifactCommentAnchor | null {
	if (!isRecord(value) || !isString(value.path) || !isString(value.tag) || !isString(value.textHash)) return null;
	if (value.path.length === 0 || value.path.length > 2000) return null;
	return {
		path: value.path,
		tag: value.tag.slice(0, 32),
		textHash: value.textHash.slice(0, 64),
		...(isString(value.excerpt) ? { excerpt: value.excerpt.slice(0, 120) } : {}),
	};
}

/**
 * A message from the bridge, checked field by field: the page in the frame is
 * untrusted and can send anything down the port it shares with the bridge.
 */
export function parseArtifactCommentsBridgeMessage(data: unknown): ArtifactCommentsFromBridge | null {
	if (!isRecord(data) || !isString(data.type)) return null;
	switch (data.type) {
		case "ready":
		case "escape":
			return { type: data.type };
		case "pick": {
			const anchor = parseAnchor(data.anchor);
			return anchor ? { type: "pick", anchor } : null;
		}
		case "marker":
			return isString(data.path) ? { type: "marker", path: data.path } : null;
		case "positions": {
			if (!Array.isArray(data.positions)) return null;
			const positions = data.positions.flatMap((item: unknown): ArtifactCommentPosition[] =>
				isRecord(item) && isString(item.path) && typeof item.top === "number" && Number.isFinite(item.top) && typeof item.found === "boolean"
					? [{ path: item.path, top: item.top, found: item.found }]
					: [],
			);
			return { type: "positions", positions };
		}
		default:
			return null;
	}
}

// ---------------------------------------------------------------------------
// Send to agent (section 10)

/** The same cap as a file annotation sent to a worker. */
export const MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH = 4096;
const MAX_MESSAGE_COMMENT_LENGTH = 1200;
const MAX_MESSAGE_NOTE_LENGTH = MAX_ARTIFACT_COMMENT_NOTE;

function compact(text: string, max: number): string {
	const flat = text.replace(/\s+/g, " ").trim();
	return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

/** The owner's message to the session that made the file, for one thread. */
export function formatArtifactCommentMessage(
	thread: ArtifactCommentThread,
	artifact: { path: string; version: number },
	note?: string,
): string {
	const where = [`<${thread.anchor.tag}>`, thread.anchor.excerpt ? `"${compact(thread.anchor.excerpt, 120)}"` : null, `[selector: ${thread.anchor.path}]`]
		.filter(Boolean)
		.join(" ");
	const head = [
		"The owner forwarded a review comment on a shared artifact they published from this session.",
		"",
		`File: ${compact(artifact.path, 500)}  (shared link version ${artifact.version})`,
		`Where: ${where}`,
		"Thread:",
	];
	const trimmedNote = note?.trim() ?? "";
	const tail = [
		...(trimmedNote ? [`Owner's note: ${compact(trimmedNote, MAX_MESSAGE_NOTE_LENGTH)}`] : []),
		"",
		"Update the file to address this, then tell the owner what changed. They will republish it.",
	];
	const lines = thread.comments.map((comment) =>
		`- ${comment.author?.name ?? "Former user"}: ${comment.deleted ? "(comment deleted)" : compact(comment.body, MAX_MESSAGE_COMMENT_LENGTH)}`,
	);
	// Keep the frame and the note whole; drop the newest comments first when the thread is too long.
	const budget = MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH - [...head, ...tail].join("\n").length - 2;
	const kept: string[] = [];
	let used = 0;
	for (const line of lines) {
		if (used + line.length + 1 > budget) {
			kept.push(`- (${lines.length - kept.length} more not included)`);
			break;
		}
		kept.push(line);
		used += line.length + 1;
	}
	return [...head, ...kept, ...tail].join("\n").slice(0, MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH);
}
