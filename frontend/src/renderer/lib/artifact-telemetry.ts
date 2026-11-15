// Closed vocabularies for artifact telemetry. Only these enums, counts and
// durations leave the machine: never a file name, path, URL, title, session
// name, content or invitee. The daemon's ao.artifacts.registered uses the same
// file type buckets (backend/internal/telemetrymeta/artifact.go).

export const ARTIFACT_FILE_TYPES = ["html", "markdown", "pdf", "image", "video", "audio", "data", "text", "office", "code", "archive", "bundle", "other"] as const;
export type ArtifactFileType = (typeof ARTIFACT_FILE_TYPES)[number];

const EXTENSION_TYPES: Record<string, ArtifactFileType> = {};
for (const [type, extensions] of [
	["html", "html htm"],
	["markdown", "md markdown mdx"],
	["pdf", "pdf"],
	["image", "png jpg jpeg gif webp svg avif bmp ico heic tif tiff"],
	["video", "mp4 mov webm mkv avi m4v"],
	["audio", "mp3 wav m4a ogg flac aac"],
	["data", "csv tsv json jsonl xlsx xls yaml yml xml parquet sqlite db"],
	["text", "txt log rtf"],
	["office", "docx doc pptx ppt key pages numbers odt ods odp"],
	["code", "js mjs cjs ts tsx jsx py go rs java rb sh css scss swift kt c cc cpp h hpp cs php sql"],
	["archive", "zip tar gz tgz 7z rar"],
] as const) {
	for (const extension of extensions.split(" ")) EXTENSION_TYPES[extension] = type;
}

/** The artifact's coarse file type, from its extension; a bundle directory row is "bundle". */
export function artifactFileType(path: string, bundle = false): ArtifactFileType {
	if (bundle) return "bundle";
	const name = path.slice(path.lastIndexOf("/") + 1);
	const dot = name.lastIndexOf(".");
	if (dot <= 0) return "other";
	return EXTENSION_TYPES[name.slice(dot + 1).toLowerCase()] ?? "other";
}

export const ARTIFACT_COUNT_BUCKETS = ["0", "1", "2-5", "6-20", "21-50", "51-200", "200+"] as const;

export function artifactCountBucket(count: number): (typeof ARTIFACT_COUNT_BUCKETS)[number] {
	if (count <= 0) return "0";
	if (count === 1) return "1";
	if (count <= 5) return "2-5";
	if (count <= 20) return "6-20";
	if (count <= 50) return "21-50";
	if (count <= 200) return "51-200";
	return "200+";
}

export const ARTIFACT_SOURCES = ["local", "remote_host", "cloud"] as const;
export type ArtifactSource = (typeof ARTIFACT_SOURCES)[number];

/** Where the artifact's session runs. */
export function artifactSource(where: { cloud?: unknown; hostId?: string | null }): ArtifactSource {
	if (where.cloud) return "cloud";
	return where.hostId ? "remote_host" : "local";
}

/** Where an artifact was opened from. */
export const ARTIFACT_OPEN_SURFACES = ["page", "tab", "chat_card", "link", "board"] as const;
export type ArtifactOpenSurface = (typeof ARTIFACT_OPEN_SURFACES)[number];

/** Which artifact list was opened: the Artifacts page or a session's Artifacts tab. */
export const ARTIFACT_LIST_SURFACES = ["page", "tab"] as const;
/** loaded: the first card painted; empty/failed: settled without one; left: closed before it settled. */
export const ARTIFACT_LIST_OUTCOMES = ["loaded", "empty", "failed", "left"] as const;

/** Where an artifact action ran: the Artifacts page, a session's tab, or the open artifact's viewer. */
export const ARTIFACT_ACTION_SURFACES = ["page", "tab", "viewer"] as const;
export type ArtifactActionSurface = (typeof ARTIFACT_ACTION_SURFACES)[number];

export const ARTIFACT_EXTERNAL_TARGETS = ["file_manager", "terminal", "browser", "download"] as const;
export const ARTIFACT_SHARE_ACCESSES = ["restricted", "anyone"] as const;
/** Where a public link was copied: the publish panel or the artifact's ⋯ menu. */
export const ARTIFACT_LINK_COPY_SURFACES = ["panel", "menu"] as const;
/** Why a publish did not go through. `limit` is the plan's link limit. */
export const ARTIFACT_PUBLISH_FAILURES = ["limit", "unsupported", "not_owner", "signed_out", "error"] as const;
/** Whose cloud session wrote the artifact: the person looking at it, or another member of their organization. */
export const ARTIFACT_OWNERS = ["self", "teammate"] as const;

/**
 * Whose a cloud artifact is, or undefined for a local or remote-host one, or
 * while either account is unknown. Only the comparison is sent, never an ID.
 */
export function artifactOwner(cloud: { createdByUserId?: string } | undefined | null, cloudUserId: string | undefined): (typeof ARTIFACT_OWNERS)[number] | undefined {
	if (!cloud?.createdByUserId || !cloudUserId) return undefined;
	return cloud.createdByUserId === cloudUserId ? "self" : "teammate";
}
