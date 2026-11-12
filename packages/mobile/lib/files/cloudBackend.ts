import { formatFileAnnotationMessages } from "../../../shared/files/file-annotations";
import type { CloudClient } from "../cloud/client";
import { cloudErrorMessage } from "../cloud/errors";
import type { CloudWorkspaceFileSummary, CloudWorkspaceStatus } from "../cloud/types";
import type { WorkspaceFileStatus, WorkspaceFileSummary, WorkspaceManifest } from "./api";
import type { FilesBackend } from "./backend";

// A cloud session's worktree for the Files screens, as the desktop reads it
// (useCloudWorkspaceReview): the control plane's provider-neutral review
// routes, answered by the session's worker. Their shapes are the daemon's
// with a few more statuses and fields, which this maps away.

/** A copy is a new file, and so is an untracked one, as the desktop shows them. */
export function workspaceStatus(status: CloudWorkspaceStatus | undefined): WorkspaceFileStatus {
	if (status === "copied" || status === "untracked") return "added";
	return status ?? "unmodified";
}

function fileSummary(file: CloudWorkspaceFileSummary): WorkspaceFileSummary {
	return {
		path: file.path,
		previousPath: file.previousPath,
		status: workspaceStatus(file.status),
		additions: file.additions,
		deletions: file.deletions,
		binary: file.binary,
		size: file.size,
		fileFingerprint: file.fileFingerprint,
	};
}

/**
 * A new file's whole text as an all-added diff, as git shows one. The worker's
 * diff leaves out a file git does not track yet (its diff is empty), where a
 * machine's daemon shows every line as added; the Changes view reads the diff.
 */
export function addedFileDiff(path: string, content: string): string {
	const lines = content.replace(/\r\n?/g, "\n").split("\n");
	const endsWithNewline = lines.at(-1) === "";
	if (endsWithNewline) lines.pop();
	if (!lines.length) return "";
	return [
		`diff --git a/${path} b/${path}`,
		"new file mode 100644",
		"--- /dev/null",
		`+++ b/${path}`,
		`@@ -0,0 +1,${lines.length} @@`,
		...lines.map((line) => `+${line}`),
		...(endsWithNewline ? [] : ["\\ No newline at end of file"]),
	].join("\n");
}

/** The control plane's refusals in its own words; a status left on them would read as a machine's. */
async function inCloudWords<T>(run: () => Promise<T>): Promise<T> {
	try {
		return await run();
	} catch (cause) {
		if (cause instanceof Error && cause.name === "AbortError") throw cause;
		throw new Error(cloudErrorMessage(cause), { cause });
	}
}

export function cloudFilesBackend(client: CloudClient, orgId: string): FilesBackend {
	return {
		key: JSON.stringify(["cloud", client.baseUrl, orgId]),
		cloud: true,
		manifest: (sessionId, signal) => inCloudWords(async (): Promise<WorkspaceManifest> => {
			const review = await client.getWorkspaceReview(orgId, sessionId, signal);
			const section = (files: CloudWorkspaceFileSummary[] | undefined) => (files ?? []).map(fileSummary);
			return {
				sessionId,
				// The review's files are the worktree's whole inventory (the desktop
				// counts on it for its tree); the changes are the ones git sees changed.
				files: (review.files ?? []).filter((file) => file.status !== "unmodified").map(fileSummary),
				sections: {
					committed: section(review.sections?.committed),
					staged: section(review.sections?.staged),
					unstaged: section(review.sections?.unstaged),
					untracked: section(review.sections?.untracked),
				},
				summary: review.summary ?? { files: 0, additions: 0, deletions: 0 },
				truncated: Boolean(review.truncated),
				workspaceVersion: review.workspaceVersion ?? "",
				compareBaseRef: review.compareBaseRef,
				compareMode: review.compareMode,
				ahead: review.ahead,
				behind: review.behind,
				degraded: false,
			};
		}),
		file: (sessionId, path, section) => inCloudWords(async () => {
			const file = await client.getWorkspaceReviewFile(orgId, sessionId, { path, scope: section });
			const status = workspaceStatus(file.status);
			const content = file.content ?? "";
			const untrackedDiff = status === "added" && !file.diff && !file.binary && !file.deleted && !file.contentTruncated && Boolean(content);
			return {
				path: file.path,
				previousPath: file.previousPath,
				status,
				additions: file.additions,
				deletions: file.deletions,
				binary: file.binary,
				deleted: file.deleted,
				size: file.size,
				content,
				contentTruncated: Boolean(file.contentTruncated),
				diff: untrackedDiff ? addedFileDiff(file.path, content) : file.diff ?? "",
				diffTruncated: Boolean(file.diffTruncated),
				imageMediaType: file.imageMediaType,
				fileFingerprint: file.fileFingerprint ?? "",
				workspaceVersion: file.workspaceVersion ?? "",
			};
		}),
		tree: (sessionId, dir) => inCloudWords(async () => {
			const tree = await client.getWorkspaceTree(orgId, sessionId, dir);
			return {
				path: tree.path ?? dir,
				entries: (tree.entries ?? []).map((entry) => ({ ...entry, status: entry.status ? workspaceStatus(entry.status) : undefined })),
				truncated: Boolean(tree.truncated),
			};
		}),
		search: (sessionId, query) => inCloudWords(async () => {
			const found = await client.searchWorkspace(orgId, sessionId, { query, limit: 50 });
			return {
				results: (found.results ?? []).map((result) => ({ path: result.path, status: workspaceStatus(result.status), binary: result.binary, size: result.size })),
				truncated: Boolean(found.truncated || found.nextCursor),
			};
		}),
		// The control plane has no image URL: the revision route returns the
		// bytes, base64 for a binary file, which an image reads as a data URI.
		imageSource: async (sessionId, path, side, version) => {
			try {
				const revision = await client.getWorkspaceRevision(orgId, sessionId, { path, side, workspaceVersion: version || undefined });
				if (!revision.exists || revision.truncated || revision.encoding !== "base64" || !revision.mediaType?.startsWith("image/")) return undefined;
				return { uri: `data:${revision.mediaType};base64,${revision.content}` };
			} catch {
				return undefined;
			}
		},
		// The person's own words, as a message: the control plane's message
		// route takes the text alone (it refuses unknown fields).
		sendFeedback: (sessionId, comments, onSent) => inCloudWords(async () => {
			let sent = 0;
			for (const { message, count } of formatFileAnnotationMessages(comments)) {
				await client.sendMessage(orgId, sessionId, { text: message });
				sent += count;
				onSent?.(count);
			}
			return sent;
		}),
	};
}
