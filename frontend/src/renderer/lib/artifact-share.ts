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
	sessionId: string;
	path: string;
	name: string;
	rawUrl?: string;
	/** Link name parts: the session's project and what the work is about. */
	project: string;
	feature: string;
}

/**
 * Reads one local artifact file through the daemon and publishes it as the
 * signed-in user's share link. Publishing the same project, feature and file
 * name again replaces that link's file instead of creating another link.
 */
export async function publishArtifactShare(input: PublishArtifactInput, fallbackError: string): Promise<ArtifactShareLink> {
	const contentUrl = artifactContentUrl(input.sessionId, input.path, input.rawUrl);
	if (!contentUrl) throw new Error(fallbackError);
	const file = await fetch(contentUrl);
	if (!file.ok) throw new Error(fallbackError);
	const bytes = new Uint8Array(await file.arrayBuffer());
	const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
	const sha256 = Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
	const query = new URLSearchParams({ name: input.name, project: input.project, feature: input.feature });
	const response = await cloudCpUpload(input.baseUrl, `${SHARES_PATH}?${query.toString()}`, "POST", bytes, {
		"Content-Type": "application/octet-stream",
		[SHA256_HEADER]: sha256,
	});
	const payload = await response.json().catch(() => null) as {
		share?: { id: string; url: string; name: string };
		replaced?: boolean;
		code?: string;
		message?: string;
	} | null;
	if (response.status === 401) throw new ArtifactShareAuthError();
	// A control plane without share links (hosted, or the flag off) has no such
	// route and answers with a plain-text 404.
	if (response.status === 404 && payload === null) throw new ArtifactShareUnavailableError();
	if (payload?.code === "artifact_share_limit_reached") throw new ArtifactShareLimitError(payload.message ?? fallbackError);
	if (!response.ok || !payload?.share) throw new Error(payload?.message || fallbackError);
	return { id: payload.share.id, url: payload.share.url, name: payload.share.name, replaced: payload.replaced === true };
}
