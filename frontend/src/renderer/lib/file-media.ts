import { isRasterImagePath } from "./image-files";

export type FileMediaKind = "image" | "video" | "audio" | "pdf";

/** Common media containers; decoding failures stay in the viewer. */
export function fileMediaKind(path: string): FileMediaKind | undefined {
	if (isRasterImagePath(path)) return "image";
	if (/\.(m4v|mov|mp4|ogv|webm)$/i.test(path)) return "video";
	if (/\.(aac|flac|m4a|mp3|oga|ogg|opus|wav)$/i.test(path)) return "audio";
	if (/\.pdf$/i.test(path)) return "pdf";
	return undefined;
}

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
