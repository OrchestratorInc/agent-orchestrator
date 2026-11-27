// Cleanup applied to every string an analytics event carries: local URLs and
// absolute paths are replaced. Shared so Electron main applies the same rules
// as the renderer to anything it posts itself.

export const REDACTED_LOCAL_URL = "[redacted-local-url]";
export const REDACTED_LOCAL_PATH = "[redacted-local-path]";
const EMBEDDED_LOCAL_URL_PATTERN =
	/(?:\bfile:\/\/\/\S+|\bapp:\/\/renderer\/\S+|\bhttps?:\/\/(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\S*)/gi;

export function isLoopbackHostname(value: string): boolean {
	const hostname = value.toLowerCase().replace(/^\[(.*)\]$/, "$1");
	return hostname === "localhost" || hostname.endsWith(".localhost") || hostname === "127.0.0.1" || hostname === "::1";
}

function isLocalURL(value: string): boolean {
	try {
		const url = new URL(value);
		return (
			url.protocol === "file:" ||
			(url.protocol === "app:" && url.host === "renderer") ||
			isLoopbackHostname(url.hostname)
		);
	} catch {
		return false;
	}
}

function redactEmbeddedAbsolutePaths(value: string): string {
	return value
		.replace(/(?:\/Users\/|\/home\/|\/tmp\/|\/private\/var\/|\/var\/folders\/)\S+/g, REDACTED_LOCAL_PATH)
		// A Windows drive path with either separator: C:\repo or C:/repo.
		.replace(/\b[A-Za-z]:[\\/][^\s)]+/g, REDACTED_LOCAL_PATH);
}

export function sanitizeSensitiveString(value: string): string {
	const trimmed = value.trim();
	if (!trimmed) return trimmed;
	if (isLocalURL(trimmed)) return REDACTED_LOCAL_URL;
	return redactEmbeddedAbsolutePaths(trimmed.replace(EMBEDDED_LOCAL_URL_PATTERN, REDACTED_LOCAL_URL));
}
