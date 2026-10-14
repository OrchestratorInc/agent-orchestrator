import { readRenderTheme, renderThemeFragment, type RenderTheme } from "./render-frame";

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

/**
 * An artifact preview URL with the app theme in its fragment, which the page's
 * theme bootstrap reads before first paint, as a framed render does. Any other
 * URL is returned as it is.
 */
export function withArtifactPreviewTheme(url: string, theme?: RenderTheme): string {
	if (!isArtifactPreviewUrl(url)) return url;
	return `${url.split("#", 1)[0]}${renderThemeFragment(theme ?? readRenderTheme(), "fullscreen")}`;
}
