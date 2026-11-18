// The format checks are shared with the mobile app, so both preview the same files.
export { isMermaidPath, isRasterImagePath, isSvgPath } from "../../../../packages/shared/files/file-formats";

/**
 * An SVG's source as an `<img>` URL. An image context runs no scripts and loads
 * no external resources, so an agent-written SVG can't act on the app.
 */
export function svgDataUrl(content: string): string {
	return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(content)}`;
}

// A light checkerboard so transparent pixels read as transparent instead of
// borrowing the panel background.
export const IMAGE_CHECKERBOARD =
	"repeating-conic-gradient(color-mix(in srgb, currentColor 8%, transparent) 0% 25%, transparent 0% 50%) 0 0 / 16px 16px";
