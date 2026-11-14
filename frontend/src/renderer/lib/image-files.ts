export { isMermaidPath, isRasterImagePath, isSvgPath } from "../../../../packages/shared/files/file-formats";

export function svgDataUrl(content: string): string {
	return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(content)}`;
}

export const IMAGE_CHECKERBOARD =
	"repeating-conic-gradient(color-mix(in srgb, currentColor 8%, transparent) 0% 25%, transparent 0% 50%) 0 0 / 16px 16px";
