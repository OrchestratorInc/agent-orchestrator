export interface CloudShareFile {
	baseUrl: string;
	source?: "local" | "cloud";
	orgId?: string;
	sessionId: string;
	path: string;
	projectId: string;
	artifactId?: string;
	shareId?: string;
	fingerprint?: string;
	access?: CloudShareAccess;
	emails?: string[];
}

export type CloudShareAccess = "restricted" | "anyone";

export interface CloudShareGrant {
	id: string;
	email: string;
	createdAt: string;
}

export interface CloudSessionShareFile extends CloudShareFile {
	source: "cloud";
	orgId: string;
}

export interface CloudShareLink {
	id: string;
	slug: string;
	name: string;
	contentUrl: string;
	viewerUrl?: string;
	contentType: string;
	size: number;
	sha256: string;
	createdAt: string;
	updatedAt: string;
	revokedAt?: string;
	access?: CloudShareAccess;
	grantCount?: number;
}

export function isPrivateShare(share: Pick<CloudShareLink, "access">): boolean {
	return share.access === "restricted";
}

export function artifactShareUrl(share: Pick<CloudShareLink, "viewerUrl" | "contentUrl">): string {
	return share.viewerUrl ?? share.contentUrl;
}

export interface CloudShareLinks {
	shares: CloudShareLink[];
	limit: number;
	used: number;
	unlimited: boolean;
	privateLinks: boolean;
}

export interface CloudShareInspection {
	name: string;
	size?: number;
	unsupported: boolean;
	shareId?: string;
	fingerprint: string;
	publishedFingerprint?: string;
}

export function sameShareFingerprint(recorded: string, current: string): boolean {
	return recorded === current || (current !== "" && recorded.endsWith(`:${current}`));
}

export type CloudSharePublishResult =
	| { status: "published"; share: CloudShareLink; replaced: boolean }
	| { status: "cancelled" }
	| { status: "changed"; inspection: CloudShareInspection }
	| { status: "limit"; message: string }
	| { status: "unsupported"; message: string }
	| { status: "not_owner" }
	| { status: "error"; message: string }
	| { status: "signed_out" };

export interface CloudShareProgress {
	sessionId: string;
	path: string;
	sentBytes: number;
	totalBytes: number;
}

export const CLOUD_SHARE_PROGRESS_CHANNEL = "cloudShare:progress";
