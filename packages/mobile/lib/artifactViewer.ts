import { artifactViewerOrigins } from "../../shared/artifacts/shared-artifact-link";

/**
 * The artifact pages the phone opens from ao-app://shared-artifact:
 * production's, plus a staging viewer's (EXPO_PUBLIC_AO_ARTIFACT_VIEWER_ORIGIN).
 * A link to any other site opens nothing.
 */
export const ARTIFACT_VIEWER_ORIGINS = artifactViewerOrigins(process.env.EXPO_PUBLIC_AO_ARTIFACT_VIEWER_ORIGIN);

/** The header's title: the host shown, when it is not a viewer (such as sign-in). */
export function artifactScreenTitle(current: string | null): string {
	try {
		const url = current ? new URL(current) : null;
		if (url && !ARTIFACT_VIEWER_ORIGINS.includes(url.origin)) return url.host;
	} catch {
		// Not a URL yet: the artifact's own title.
	}
	return "Shared artifact";
}
