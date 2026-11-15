export { fileMediaKind, type FileMediaKind } from "../../../../packages/shared/files/file-formats";

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
