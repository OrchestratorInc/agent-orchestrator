import { isFileViewCapture, type ChatFileReference, type FileAnnotationRange, type FileAnnotationResolution } from "../../shared/chat-file-reference";
import type { FileOpenOptions } from "../components/FileContentPane";
import type { FilesSource } from "../hooks/useSessionWorkspaceFiles";

/**
 * The lines a resolved reference reveals: its captured rows moved by the
 * resolution's offsets. A changed reference opens its file without them.
 */
export function fileAnnotationRange(reference: ChatFileReference, resolution: FileAnnotationResolution): FileAnnotationRange | undefined {
	if (resolution.state === "changed") return undefined;
	return {
		...reference,
		line: resolution.line ?? reference.line,
		endLine: resolution.endLine ?? reference.endLine,
		lines: reference.lines.map((row) => ({
			...row,
			oldNo: row.oldNo == null ? null : row.oldNo + (resolution.oldLineOffset ?? 0),
			newNo: row.newNo == null ? null : row.newNo + (resolution.newLineOffset ?? 0),
		})),
		requestKey: 0,
	};
}

/**
 * Where a Cloud session opens a sent reference: its file in the worktree (or
 * the commit it was captured from), or a PR's file read as a view of that PR.
 * Undefined when there is nothing to open (an unavailable reference, or an
 * artifact, which the Files review does not show).
 */
export function cloudFileAnnotationOpen(reference: ChatFileReference, resolution: FileAnnotationResolution): { path: string; options: FileOpenOptions } | undefined {
	if (resolution.state === "unavailable" || reference.source.kind === "artifact") return undefined;
	const path = resolution.path ?? reference.path;
	const number = reference.source.kind === "pull_request" ? reference.source.number : undefined;
	const source: FilesSource | undefined = number
		? {
			kind: "snapshot",
			view: { prNumber: number, sourceUrl: reference.source.url || undefined },
			commitShas: reference.commitSha ? [reference.commitSha] : [],
			label: reference.commitSha ? `PR #${number} · ${reference.commitSha.slice(0, 7)}` : `PR #${number}`,
		}
		: undefined;
	return {
		path,
		options: {
			source,
			previousPath: reference.previousPath,
			commitSha: source ? undefined : reference.commitSha,
			scope: (source ? "committed" : reference.scope) as FileOpenOptions["scope"],
			mode: isFileViewCapture(reference) ? "file" : "diff",
			annotationRange: fileAnnotationRange(reference, resolution),
		},
	};
}
