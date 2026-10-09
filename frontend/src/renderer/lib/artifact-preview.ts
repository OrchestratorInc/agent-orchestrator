const ARTIFACT_PREVIEW_HOST_LABEL = "ao-preview-artifact";

/**
 * True for a URL on the daemon's session-artifact preview origin
 * (`ao-preview-artifact.<id>.localhost`). Artifact files are static output the
 * daemon serves the same way whether or not the session is still running.
 */
export function isArtifactPreviewUrl(url: string | undefined): boolean {
	if (!url) return false;
	try {
		return new URL(url).hostname.startsWith(`${ARTIFACT_PREVIEW_HOST_LABEL}.`);
	} catch {
		return false;
	}
}
