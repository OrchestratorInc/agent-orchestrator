// Wire types for comments on shared artifact links (Cloud API
// /artifact-shares/{linkId}/comments*). They mirror the ArtifactShareComment*
// schemas in contracts/cloud/openapi.yaml; a compile-time test in the desktop
// keeps the two equal. This file has no imports so the web viewer (ao-landing)
// can vendor it unchanged.

export const ARTIFACT_COMMENT_MAX_BODY_LENGTH = 5000;
export const ARTIFACT_COMMENT_MAX_EXCERPT_LENGTH = 120;
export const ARTIFACT_COMMENT_MAX_PATH_LENGTH = 2048;
export const ARTIFACT_COMMENT_TEXT_HASH_LENGTH = 16;
/** How many characters of normalised element text the text hash covers. */
export const ARTIFACT_COMMENT_TEXT_HASH_SOURCE_LENGTH = 200;
/** How often an open comments panel asks "anything new?" (with If-None-Match). */
export const ARTIFACT_COMMENTS_POLL_INTERVAL_MS = 30_000;

/** `body>main:1>h1:1`: tag and 1-based position among same-tag element siblings, from <body>. */
export const ARTIFACT_COMMENT_PATH_PATTERN = /^body(>[A-Za-z][A-Za-z0-9-]*:[1-9][0-9]*)*$/;
export const ARTIFACT_COMMENT_TAG_PATTERN = /^[A-Za-z][A-Za-z0-9-]*$/;
export const ARTIFACT_COMMENT_TEXT_HASH_PATTERN = /^[0-9a-f]{16}$/;

export type ArtifactCommentAnchor = {
	path: string;
	tag: string;
	/** First 16 hex chars of sha256(first 200 chars of normalised text). */
	textHash: string;
	/** ≤120 chars of the element's text; for the agent message only, never shown in the UI. */
	excerpt: string;
};

/**
 * A commenter by display name only, never email. The server fills `name`:
 * their display name, "Orchestrator user" without one, "Former user" once
 * their account is deleted (then `id` is null).
 */
export type ArtifactCommentAuthor = { id: string | null; name: string };

export type ArtifactCommentThreadStatus = "open" | "resolved";

export type ArtifactComment = {
	id: string;
	threadId: string;
	author: ArtifactCommentAuthor;
	clientId: string;
	/** Plain text; empty when deleted. */
	body: string;
	createdAt: string;
	editedAt: string | null;
	deleted: boolean;
};

export type ArtifactCommentThread = {
	id: string;
	version: number;
	status: ArtifactCommentThreadStatus;
	anchor: ArtifactCommentAnchor;
	createdAt: string;
	/** When the thread itself last changed: created, resolved, reopened or sent. */
	updatedAt: string;
	/** null while open. */
	resolvedBy: ArtifactCommentAuthor | null;
	resolvedAt: string | null;
	sentToAgentAt: string | null;
	sentToAgentCount: number;
	/** Oldest first; the first is the thread's own comment. */
	comments: ArtifactComment[];
};

export type ArtifactCommentsSnapshot = {
	version: number;
	currentVersion: number;
	revision: number;
	you: { userId: string; isOwner: boolean };
	threads: ArtifactCommentThread[];
};

export type ArtifactThreadCreateInput = {
	version: number;
	anchor: ArtifactCommentAnchor;
	body: string;
	clientId: string;
};
export type ArtifactReplyInput = { body: string; clientId: string };
export type ArtifactThreadStatusInput = { status: ArtifactCommentThreadStatus };
export type ArtifactCommentEditInput = { body: string };

/** Error codes the comment routes return in the standard error envelope. */
export const ARTIFACT_COMMENT_ERROR_CODES = {
	notFound: "not_found",
	invalidRequest: "invalid_request",
	forbidden: "forbidden",
	versionUnknown: "artifact_share_version_unknown",
	/** 409: this clientId was already used for a different comment; make a new one. */
	clientIdConflict: "conflict",
	/** 413: the request body is over 32 KiB. */
	requestTooLarge: "request_too_large",
} as const;

/**
 * What a commenter is called. The server already fills the name; this only
 * covers a blank one: "Former user" for a deleted account (id null),
 * otherwise "Orchestrator user".
 */
export function artifactCommentAuthorLabel(author: ArtifactCommentAuthor): string {
	const name = author.name.trim();
	if (name !== "") return name;
	return author.id === null ? "Former user" : "Orchestrator user";
}

/** Length in Unicode code points, which is what the 5000 cap counts (Postgres char_length). */
export function artifactCommentLength(body: string): number {
	let count = 0;
	for (const _ of body) count += 1;
	return count;
}

export type ArtifactCommentBodyProblem = "empty" | "too_long";

/** null when the body may be sent; the server answers 400 invalid_request otherwise. */
export function artifactCommentBodyProblem(body: string): ArtifactCommentBodyProblem | null {
	if (body.trim() === "") return "empty";
	if (artifactCommentLength(body) > ARTIFACT_COMMENT_MAX_BODY_LENGTH) return "too_long";
	return null;
}

export function isArtifactCommentAnchor(value: unknown): value is ArtifactCommentAnchor {
	if (typeof value !== "object" || value === null) return false;
	const anchor = value as Record<string, unknown>;
	return (
		typeof anchor.path === "string" &&
		anchor.path.length <= ARTIFACT_COMMENT_MAX_PATH_LENGTH &&
		ARTIFACT_COMMENT_PATH_PATTERN.test(anchor.path) &&
		typeof anchor.tag === "string" &&
		anchor.tag.length <= 64 &&
		ARTIFACT_COMMENT_TAG_PATTERN.test(anchor.tag) &&
		typeof anchor.textHash === "string" &&
		ARTIFACT_COMMENT_TEXT_HASH_PATTERN.test(anchor.textHash) &&
		typeof anchor.excerpt === "string" &&
		artifactCommentLength(anchor.excerpt) <= ARTIFACT_COMMENT_MAX_EXCERPT_LENGTH
	);
}

/** The ETag the list route sends: `"r<revision>-v<version>"`, quoted. */
export function artifactCommentsEtag(revision: number, version: number): string {
	return `"r${revision}-v${version}"`;
}

/** Reads an ETag back (quoted or not, with or without W/); null when it is not one of ours. */
export function parseArtifactCommentsEtag(etag: string): { revision: number; version: number } | null {
	const match = /^(?:W\/)?"?r(\d+)-v(\d+)"?$/.exec(etag.trim());
	if (match === null) return null;
	return { revision: Number(match[1]), version: Number(match[2]) };
}

/** Whitespace collapsed and trimmed: the text the hash and excerpt are taken from. */
export function normalizeArtifactAnchorText(text: string): string {
	return text.replace(/\s+/g, " ").trim();
}

function firstCodePoints(text: string, count: number): string {
	let out = "";
	let taken = 0;
	for (const char of text) {
		if (taken === count) break;
		out += char;
		taken += 1;
	}
	return out;
}

/** The anchor excerpt: the first 120 code points of the element's normalised text. */
export function artifactAnchorExcerpt(text: string): string {
	return firstCodePoints(normalizeArtifactAnchorText(text), ARTIFACT_COMMENT_MAX_EXCERPT_LENGTH);
}

/**
 * The anchor text hash: SHA-256 over the UTF-8 of the first 200 code points
 * of the element's normalised text, as lowercase hex, first 16 characters.
 * The comment bridge computes the same value inside the artifact frame.
 */
export async function artifactAnchorTextHash(text: string): Promise<string> {
	const source = firstCodePoints(normalizeArtifactAnchorText(text), ARTIFACT_COMMENT_TEXT_HASH_SOURCE_LENGTH);
	const digest = await globalThis.crypto.subtle.digest("SHA-256", new TextEncoder().encode(source));
	let hex = "";
	for (const byte of new Uint8Array(digest)) hex += byte.toString(16).padStart(2, "0");
	return hex.slice(0, ARTIFACT_COMMENT_TEXT_HASH_LENGTH);
}
