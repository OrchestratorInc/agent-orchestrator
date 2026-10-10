import type { CloudCpClient } from "./cloud-cp";
import { clientForSessionHost } from "./host-clients";
import { isMarkdownPreviewPath } from "./markdown-frontmatter";

/** File identity already used to open a Markdown link. External URLs are not a source. */
export type MarkdownFilePreviewSource =
	| { kind: "workspace"; sessionId: string; hostId?: string }
	| { kind: "cloud"; orgId: string; sessionId: string };

export function markdownFilePreviewSource(sessionId: string, hostId: string | undefined, cloudOrgId: string | undefined): MarkdownFilePreviewSource {
	if (cloudOrgId) return { kind: "cloud", orgId: cloudOrgId, sessionId };
	return { kind: "workspace", sessionId, hostId };
}

export async function loadMarkdownPreviewText(
	source: MarkdownFilePreviewSource,
	path: string,
	cloud?: { client: CloudCpClient; ready: boolean },
): Promise<string> {
	if (!isMarkdownPreviewPath(path) || path.split("/").includes("..")) {
		throw new Error("Unable to load this file.");
	}
	if (source.kind === "cloud") {
		if (!cloud?.ready) throw new Error("Unable to load this file.");
		const file = await cloud.client.getWorkspaceReviewFile(source.orgId, source.sessionId, { path, scope: "combined" });
		if (file.binary || file.deleted) throw new Error("Unable to load this file.");
		return file.content ?? "";
	}
	const { data, error } = await clientForSessionHost(source.hostId).GET("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId: source.sessionId }, query: { path } },
	});
	if (error || !data || data.binary || data.deleted) throw new Error("Unable to load this file.");
	return data.content ?? "";
}
