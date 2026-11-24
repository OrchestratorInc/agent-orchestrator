import { AoError } from "../../../../packages/shared/telemetry/ao-error";
import { resolveSessionWorkspacePath } from "../hooks/useSessionWorkspaceFiles";
import type { CloudCpClient } from "./cloud-cp";
import { splitWorkspaceRepositoryPath, withWorkspaceRepository } from "./cloud-cp";
import { clientForSessionHost } from "./host-clients";
import { isMarkdownFilePath, stripMarkdownPreviewAnchor } from "./markdown-frontmatter";
import { isAbsoluteFileReference } from "./workspace-file-path";

/** File identity already used to open a Markdown link. External URLs are not a source. */
export type MarkdownFilePreviewSource =
	| { kind: "workspace"; sessionId: string; hostId?: string }
	| { kind: "cloud"; orgId: string; sessionId: string };

export function markdownFilePreviewSource(sessionId: string, hostId: string | undefined, cloudOrgId: string | undefined): MarkdownFilePreviewSource {
	if (cloudOrgId) return { kind: "cloud", orgId: cloudOrgId, sessionId };
	return { kind: "workspace", sessionId, hostId };
}

function hasParentTraversal(path: string): boolean {
	return path.split(/[\\/]/).includes("..");
}

export async function loadMarkdownPreviewText(
	source: MarkdownFilePreviewSource,
	rawPath: string,
	cloud?: { client: CloudCpClient; ready: boolean },
): Promise<string> {
	const path = stripMarkdownPreviewAnchor(rawPath);
	if (!isMarkdownFilePath(path)) throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
	if (source.kind === "cloud") {
		if (!cloud?.ready) throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
		// A sibling repository is addressed as ../<repository>/<path>; the client
		// must be scoped to it before the remaining path is read.
		const { repository, path: relativePath } = splitWorkspaceRepositoryPath(path);
		if (hasParentTraversal(relativePath)) throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
		const file = await withWorkspaceRepository(cloud.client, repository)
			.getWorkspaceReviewFile(source.orgId, source.sessionId, { path: relativePath, scope: "combined" });
		if (file.binary || file.deleted) throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
		return file.content ?? "";
	}
	// An absolute path is only a workspace file if the daemon says so; clicking
	// the link resolves it the same way before opening.
	let workspacePath = path;
	if (isAbsoluteFileReference(path)) {
		const resolved = await resolveSessionWorkspacePath(source.sessionId, path, source.hostId);
		if (resolved?.kind !== "workspace") throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
		workspacePath = resolved.path;
	}
	if (hasParentTraversal(workspacePath)) throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
	const { data, error } = await clientForSessionHost(source.hostId).GET("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId: source.sessionId }, query: { path: workspacePath } },
	});
	if (error || !data || data.binary || data.deleted) throw new AoError("MARKDOWN_PREVIEW_LOAD_FAILED", "Unable to load this file.");
	return data.content ?? "";
}
