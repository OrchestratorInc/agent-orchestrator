// The format lists are shared with the mobile app, so both preview the same files.
export { fileMediaKind, type FileMediaKind } from "../../../../packages/shared/files/file-formats";

/**
 * Internal media must carry the renderer Origin through the daemon/proxy guard.
 * Keep ordinary external images in their existing no-CORS mode.
 */
export function mediaCrossOrigin(src: string | undefined): "anonymous" | undefined {
	if (!src) return undefined;
	try {
		const url = new URL(src);
		const local = url.hostname === "127.0.0.1" || url.hostname === "[::1]" || url.hostname === "localhost" || url.hostname.endsWith(".localhost");
		return local && (url.protocol === "http:" || url.protocol === "https:") ? "anonymous" : undefined;
	} catch {
		return undefined;
	}
}
