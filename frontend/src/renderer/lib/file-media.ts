import { isRasterImagePath } from "./image-files";

export type FileMediaKind = "image" | "video" | "audio";

/** Common media containers; decoding failures stay in the viewer. */
export function fileMediaKind(path: string): FileMediaKind | undefined {
	if (isRasterImagePath(path)) return "image";
	if (/\.(m4v|mov|mp4|ogv|webm)$/i.test(path)) return "video";
	if (/\.(aac|flac|m4a|mp3|oga|ogg|opus|wav)$/i.test(path)) return "audio";
	return undefined;
}
