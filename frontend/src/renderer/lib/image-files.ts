/** Raster formats the app previews as images, matching the daemon's workspace blob route. */
export function isRasterImagePath(path: string): boolean {
	return /\.(apng|avif|bmp|gif|ico|jpe?g|png|webp)$/i.test(path);
}

const RASTER_IMAGE_MEDIA_TYPES: Record<string, string> = {
	apng: "image/apng",
	avif: "image/avif",
	bmp: "image/bmp",
	gif: "image/gif",
	ico: "image/x-icon",
	jpeg: "image/jpeg",
	jpg: "image/jpeg",
	png: "image/png",
	webp: "image/webp",
};

/** The extension-derived media type for a raster image path, e.g. shot.png → image/png. */
export function rasterImageMediaType(path: string): string | undefined {
	const match = /\.([a-z0-9]+)$/i.exec(path);
	return match ? RASTER_IMAGE_MEDIA_TYPES[match[1].toLowerCase()] : undefined;
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
