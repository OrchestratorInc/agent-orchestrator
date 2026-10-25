import type { CloudCpWorkspaceReviewResponse } from "./cloud-cp";
import { isAbsoluteFileReference, normalizeWorkspaceFileReference } from "./workspace-file-path";

export function cloudWorkspaceFilePath(rawPath: string, review: CloudCpWorkspaceReviewResponse): string {
	const reference = normalizeWorkspaceFileReference(rawPath);
	if (!reference || reference.includes("\0") || reference.split("/").some((part) => part === "." || part === "..")) return reference;
	if (/^file:\/\//i.test(rawPath.trim())) {
		try {
			if (new URL(rawPath.trim()).hostname) return rawPath;
		} catch {
			return rawPath;
		}
	} else if (/^[A-Za-z][A-Za-z\d+.-]*:/.test(reference) || reference.startsWith("//")) return reference;
	if (isAbsoluteFileReference(reference)) {
		const root = review.workspaceRoot?.replace(/\/$/, "");
		return root && reference.startsWith(`${root}/`) ? reference.slice(root.length + 1) : reference;
	}
	if (reference.includes("/") || review.files.some((file) => file.path === reference)) return reference;
	if (review.truncated) return reference;
	const matches = review.files.filter((file) => file.path.split("/").at(-1) === reference);
	return matches.length === 1 ? matches[0]!.path : reference;
}
