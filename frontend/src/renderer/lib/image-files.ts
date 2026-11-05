/** Raster formats the app previews as images, matching the daemon's workspace blob route. */
export function isRasterImagePath(path: string): boolean {
	return /\.(apng|avif|bmp|gif|ico|jpe?g|png|webp)$/i.test(path);
}

/** A Mermaid diagram source file, which renders as its diagram. */
export function isMermaidPath(path: string): boolean {
	return /\.(mmd|mermaid)$/i.test(path);
}

/** SVG is text, so it keeps its code view and gains a rendered one. */
export function isSvgPath(path: string): boolean {
	return /\.svg$/i.test(path);
}

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
