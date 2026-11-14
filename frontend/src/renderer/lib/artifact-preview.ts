import { sessionIsActive, type WorkspaceSession } from "../types/workspace";

const ARTIFACT_PREVIEW_HOST_LABEL = "ao-preview-artifact";

export function isArtifactPreviewUrl(url: string | undefined): boolean {
	if (!url) return false;
	try {
		return new URL(url).hostname.startsWith(`${ARTIFACT_PREVIEW_HOST_LABEL}.`);
	} catch {
		return false;
	}
}

export function sessionBrowserUnavailable(session: WorkspaceSession | undefined, hostId = session?.hostId): boolean {
	return Boolean(session && !sessionIsActive(session) && !(isArtifactPreviewUrl(session.previewUrl) && !hostId));
}
