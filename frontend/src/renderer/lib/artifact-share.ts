import { cloudCpUpload } from "./cloud-cp/renderer-client";
import { artifactContentUrl } from "./artifact-content-url";

const SHARES_PATH = "/api/cloud/v1/me/artifact-shares";
const SHA256_HEADER = "X-AO-Artifact-SHA256";

export interface ArtifactShareLink {
	id: string;
	url: string;
	name: string;
	/** True when this publish replaced the version of the document's existing link. */
	replaced: boolean;
}

/** The user already holds their limit of share links; the control plane refused a new one. */
export class ArtifactShareLimitError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "ArtifactShareLimitError";
	}
}

/** The control plane did not accept the cloud session: signed out or expired. */
export class ArtifactShareAuthError extends Error {
	constructor() {
		super("unauthenticated");
		this.name = "ArtifactShareAuthError";
	}
}

/** The signed-in control plane does not offer share links. */
export class ArtifactShareUnavailableError extends Error {
	constructor() {
		super("unavailable");
		this.name = "ArtifactShareUnavailableError";
	}
}

export interface PublishArtifactInput {
	baseUrl: string;
	/** The signed-in cloud user; the link this file was shared as is remembered per user. */
	userId: string;
	sessionId: string;
	path: string;
	name: string;
	rawUrl?: string;
	/** Link name parts for a first publish: the session's project and what the work is about. */
	project: string;
	feature: string;
}

// The link each local artifact file was last shared as, so sharing it again
// updates that link even after the session is renamed. Keyed by control plane,
// user, session and path.
const SHARE_IDS_STORAGE_KEY = "ao.artifactShareIds.v1";
const MAX_REMEMBERED_SHARES = 500;

function shareIdKey(input: PublishArtifactInput): string {
	return JSON.stringify([input.baseUrl, input.userId, input.sessionId, input.path]);
}

function readShareIds(): Record<string, string> {
	try {
		const entries = JSON.parse(window.localStorage.getItem(SHARE_IDS_STORAGE_KEY) ?? "{}") as unknown;
		return entries && typeof entries === "object" && !Array.isArray(entries) ? entries as Record<string, string> : {};
	} catch {
		return {};
	}
}

function writeShareId(key: string, id: string | undefined): void {
	try {
		const entries = readShareIds();
		delete entries[key];
		if (id) entries[key] = id;
		// Insertion order is recency, so the oldest files drop first.
		const keys = Object.keys(entries);
		for (const stale of keys.slice(0, Math.max(0, keys.length - MAX_REMEMBERED_SHARES))) delete entries[stale];
		window.localStorage.setItem(SHARE_IDS_STORAGE_KEY, JSON.stringify(entries));
	} catch {
		// Storage can be full or blocked; the next share then publishes by name.
	}
}

type SharePayload = {
	share?: { id: string; url: string; name: string };
	replaced?: boolean;
	code?: string;
	message?: string;
} | null;

/**
 * Reads one local artifact file through the daemon and publishes it as the
 * signed-in user's share link. A file shared before from this machine updates
 * its remembered link with PUT, so renaming the session keeps the same link
 * and slot. Otherwise it publishes by project, feature and file name, where
 * the same document again replaces its link's file.
 */
export async function publishArtifactShare(input: PublishArtifactInput, fallbackError: string): Promise<ArtifactShareLink> {
	const contentUrl = artifactContentUrl(input.sessionId, input.path, input.rawUrl);
	if (!contentUrl) throw new Error(fallbackError);
	const file = await fetch(contentUrl);
	if (!file.ok) throw new Error(fallbackError);
	const bytes = new Uint8Array(await file.arrayBuffer());
	const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
	const sha256 = Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
	const headers = { "Content-Type": "application/octet-stream", [SHA256_HEADER]: sha256 };
	const key = shareIdKey(input);

	const known = readShareIds()[key];
	if (known) {
		const query = new URLSearchParams({ name: input.name });
		const response = await cloudCpUpload(input.baseUrl, `${SHARES_PATH}/${encodeURIComponent(known)}?${query.toString()}`, "PUT", bytes, headers);
		const payload = await readPayload(response);
		// The remembered link is gone (revoked or deleted) or names another file:
		// forget it and publish by name.
		const stale = (response.status === 404 && payload !== null) || payload?.code === "artifact_share_name_changed";
		if (!stale) {
			const link = shareLink(response, payload, fallbackError, true);
			writeShareId(key, link.id);
			return link;
		}
		writeShareId(key, undefined);
	}

	const query = new URLSearchParams({ name: input.name, project: input.project, feature: input.feature });
	const response = await cloudCpUpload(input.baseUrl, `${SHARES_PATH}?${query.toString()}`, "POST", bytes, headers);
	const link = shareLink(response, await readPayload(response), fallbackError, false);
	writeShareId(key, link.id);
	return link;
}

async function readPayload(response: Response): Promise<SharePayload> {
	return await response.json().catch(() => null) as SharePayload;
}

function shareLink(response: Response, payload: SharePayload, fallbackError: string, replaced: boolean): ArtifactShareLink {
	if (response.status === 401) throw new ArtifactShareAuthError();
	// A control plane without share links (hosted, or the flag off) has no such
	// route and answers with a plain-text 404.
	if (response.status === 404 && payload === null) throw new ArtifactShareUnavailableError();
	if (payload?.code === "artifact_share_limit_reached") throw new ArtifactShareLimitError(payload.message ?? fallbackError);
	if (!response.ok || !payload?.share) throw new Error(payload?.message || fallbackError);
	return { id: payload.share.id, url: payload.share.url, name: payload.share.name, replaced: replaced || payload.replaced === true };
}
