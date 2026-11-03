// The Orchestrator sign-in returns to ao-app://callback. On iOS the auth
// session captures that URL privately. On Android the Custom Tab delivers it to
// the activity as an ordinary deep link, so Expo Router also receives it while
// expo-web-browser completes the sign-in. There is no /callback screen.
const AUTH_CALLBACK = /^(?:ao-app|aomobile):\/\/\/?callback(?:[/?#]|$)/i;

export function isAuthCallback(path: string): boolean {
	return AUTH_CALLBACK.test(path);
}

/**
 * Keeps the sign-in redirect out of navigation. A warm link is ignored because
 * openAuthSessionAsync already consumed it; a cold start (the OS reclaimed the
 * app during sign-in) opens the app normally.
 */
export function redirectSystemPath({ path, initial }: { path: string; initial: boolean }): string | null {
	if (!isAuthCallback(path)) return path;
	return initial ? "/" : null;
}
