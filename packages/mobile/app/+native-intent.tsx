/** OAuth belongs to the pending browser session, not to screen navigation. */
export function redirectSystemPath({ path, initial }: { path: string; initial: boolean }): string | null {
	try {
		const url = new URL(path);
		const authScheme = ["ao-app:", "aoagents.ao:", "aomobile:"].includes(url.protocol);
		if (authScheme && url.hostname === "callback" && (url.pathname === "" || url.pathname === "/") && !url.username && !url.password && !url.port) {
			// This only filters Expo Router. WebBrowser still receives the original
			// Linking event and validates state/PKCE before accepting credentials.
			// A cold callback has no pending request and must not exchange its code.
			return initial ? "/" : null;
		}
	} catch {
		// Relative and malformed paths are not ours to rewrite.
	}
	return path;
}
