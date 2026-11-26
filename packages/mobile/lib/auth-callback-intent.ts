import { parseSharedArtifactLink } from "../../shared/artifacts/shared-artifact-link";
import { ARTIFACT_VIEWER_ORIGINS } from "./artifactViewer";
import { reportOpenedBy } from "./telemetry/uiUsageRegistry";

// The Orchestrator sign-in returns to ao-app://callback. On iOS the auth
// session captures that URL privately. On Android the Custom Tab delivers it to
// the activity as an ordinary deep link, so Expo Router also receives it while
// expo-web-browser completes the sign-in. There is no /callback screen. A
// coding-agent sign-in returns to ao-app://agent-login the same way.
const AUTH_CALLBACK = /^(?:ao-app|aomobile):\/\/\/?(?:callback|agent-login)(?:[/?#]|$)/i;
// A link to a session screen: `aomobile://session/<id>`, `aomobile:///session/<id>` or `/session/<id>`.
const SESSION_LINK = /^(?:[a-z][\w+.-]*:\/\/\/?|\/)session\/[^/?#]/i;

export function isAuthCallback(path: string): boolean {
	return AUTH_CALLBACK.test(path);
}

/**
 * Keeps the sign-in redirect out of navigation. A warm link is ignored because
 * openAuthSessionAsync already consumed it; a cold start (the OS reclaimed the
 * app during sign-in) opens the app normally.
 */
export function redirectSystemPath({ path, initial }: { path: string; initial: boolean }): string | null {
	const artifact = parseSharedArtifactLink(path, ARTIFACT_VIEWER_ORIGINS);
	if (artifact) return `/shared-artifact?url=${encodeURIComponent(artifact)}`;
	if (isAuthCallback(path)) return initial ? "/" : null;
	// Every link the app opens passes here first, on a cold start too, before the
	// screen it names mounts. A home-screen launch passes the app's root URL, so
	// only a link that names a session marks the session as opened from a link.
	if (SESSION_LINK.test(path)) reportOpenedBy("deep_link");
	return path;
}
