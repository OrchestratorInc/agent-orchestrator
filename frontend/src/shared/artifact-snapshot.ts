export const ARTIFACT_SNAPSHOT_WIDTH = 640;
export const ARTIFACT_SNAPSHOT_HEIGHT = 360;
export const ARTIFACT_SNAPSHOT_QUALITY = 0.8;
export const ARTIFACT_SNAPSHOT_VERSION = 1;
export const LOCAL_SNAPSHOT_HOST = "local";
export const ARTIFACT_SNAPSHOT_SCHEME = "ao-artifact-preview";
const SNAPSHOT_URL_HOST = "cache";

export type ArtifactSnapshotJob = {
	sessionId: string;
	path: string;
	name: string;
	kind: string;
	updatedAt: string;
	inlineUrl?: string;
	rawUrl?: string;
	previewUrl?: string;
	sessionTitle: string;
	cloud?: { orgId: string };
	hostId?: string;
	theme: "light" | "dark";
	styleTheme: string;
};

export type ArtifactSnapshotIdentity = Pick<ArtifactSnapshotJob, "hostId" | "cloud" | "sessionId" | "path" | "updatedAt" | "theme" | "styleTheme">;

const MAX_FIELD = 4_096;
const FIELDS = ["sessionId", "path", "name", "kind", "updatedAt", "sessionTitle", "styleTheme"] as const;
const URL_FIELDS = ["inlineUrl", "rawUrl", "previewUrl"] as const;

export function parseArtifactSnapshotJob(value: unknown): ArtifactSnapshotJob | undefined {
	if (typeof value !== "object" || value === null) return undefined;
	const input = value as Record<string, unknown>;
	for (const field of FIELDS) {
		if (typeof input[field] !== "string" || (input[field] as string).length > MAX_FIELD) return undefined;
	}
	for (const field of URL_FIELDS) {
		const url = input[field];
		if (url !== undefined && (typeof url !== "string" || url.length > MAX_FIELD)) return undefined;
	}
	const hostId = input.hostId;
	if (hostId !== undefined && (typeof hostId !== "string" || (hostId !== "" && hostId !== LOCAL_SNAPSHOT_HOST))) return undefined;
	if (!input.sessionId || !input.path || (input.theme !== "light" && input.theme !== "dark")) return undefined;
	const cloud = input.cloud;
	if (cloud !== undefined && (typeof cloud !== "object" || cloud === null || typeof (cloud as { orgId?: unknown }).orgId !== "string")) return undefined;
	return {
		sessionId: input.sessionId as string,
		path: input.path as string,
		name: input.name as string,
		kind: input.kind as string,
		updatedAt: input.updatedAt as string,
		sessionTitle: input.sessionTitle as string,
		inlineUrl: input.inlineUrl as string | undefined,
		rawUrl: input.rawUrl as string | undefined,
		previewUrl: input.previewUrl as string | undefined,
		cloud: cloud ? { orgId: (cloud as { orgId: string }).orgId } : undefined,
		hostId: hostId || undefined,
		theme: input.theme,
		styleTheme: input.styleTheme as string,
	};
}

export function artifactSnapshotUrl(identity: ArtifactSnapshotIdentity): string {
	const params = new URLSearchParams({ s: identity.sessionId, p: identity.path, u: identity.updatedAt, t: identity.theme, y: identity.styleTheme });
	if (identity.hostId) params.set("h", identity.hostId);
	if (identity.cloud) params.set("o", identity.cloud.orgId);
	return `${ARTIFACT_SNAPSHOT_SCHEME}://${SNAPSHOT_URL_HOST}/?${params}`;
}

export function parseArtifactSnapshotUrl(value: string): ArtifactSnapshotIdentity | undefined {
	if (!URL.canParse(value)) return undefined;
	const url = new URL(value);
	if (url.protocol !== `${ARTIFACT_SNAPSHOT_SCHEME}:` || url.host !== SNAPSHOT_URL_HOST) return undefined;
	const field = (name: string) => {
		const text = url.searchParams.get(name);
		return text !== null && text.length <= MAX_FIELD ? text : undefined;
	};
	const [sessionId, path, updatedAt, theme, styleTheme] = [field("s"), field("p"), field("u"), field("t"), field("y")];
	const hostId = field("h");
	const orgId = field("o");
	if (!sessionId || !path || updatedAt === undefined || styleTheme === undefined || (theme !== "light" && theme !== "dark")) return undefined;
	if (hostId !== undefined && hostId !== "" && hostId !== LOCAL_SNAPSHOT_HOST) return undefined;
	return { sessionId, path, updatedAt, theme, styleTheme, hostId: hostId || undefined, cloud: orgId !== undefined ? { orgId } : undefined };
}
