/**
 * Gallery card previews, as the desktop draws them (frontend ArtifactThumbnail):
 * a page laid out wider than the card and scaled down into it, so the card
 * shows the top of the real thing. The WebView itself is laid out at that
 * width and scaled as a view, so the page sees a real 420px window (a canvas
 * sized from `innerWidth` fills it, which a CSS zoom would not give it).
 */

/** The width an HTML card lays its page out at, in CSS pixels: a phone's, as the viewer shows it. */
export const THUMBNAIL_PAGE_WIDTH = 420;

/** How much of a text file a card reads and shows. */
export const THUMBNAIL_TEXT_CHARS = 4000;

/** Text files larger than this get no text preview. */
export const MAX_THUMBNAIL_TEXT_BYTES = 256 * 1024;

/** The scale that fits a page laid out at THUMBNAIL_PAGE_WIDTH into a card `cardWidth` points wide. */
export function thumbnailScale(cardWidth: number): number {
	return cardWidth > 0 ? cardWidth / THUMBNAIL_PAGE_WIDTH : 1;
}

/**
 * The page with its scrollbars hidden: a card shows the top of the page and
 * never scrolls. The rule goes inside <head> (or <html>), never before the
 * doctype, which would drop the page into quirks mode.
 */
export function thumbnailPageHtml(html: string): string {
	const rule = "<style>html{overflow:hidden!important}</style>";
	for (const tag of [/<head\b[^>]*>/i, /<html\b[^>]*>/i]) {
		const match = tag.exec(html);
		if (match) return html.slice(0, match.index + match[0].length) + rule + html.slice(match.index + match[0].length);
	}
	const doctype = /^\s*<!doctype[^>]*>/i.exec(html);
	return doctype ? doctype[0] + rule + html.slice(doctype[0].length) : rule + html;
}

// Text previews read once per file version; a gallery scrolls past the same cards again.
const TEXT_CACHE_SIZE = 24;
const textCache = new Map<string, Promise<string | undefined>>();

/**
 * A text card's preview for `key` (which carries the file's version), read
 * once. `undefined` means the file has nothing to show (binary or empty) and
 * is kept; a failed read (offline, a timeout) also reads as `undefined` but is
 * not kept, so the next mount or refresh tries again.
 */
export function cachedThumbnailText(key: string, read: () => Promise<string | undefined>): Promise<string | undefined> {
	const hit = textCache.get(key);
	if (hit) return hit;
	const pending: Promise<string | undefined> = read().catch(() => {
		if (textCache.get(key) === pending) textCache.delete(key);
		return undefined;
	});
	textCache.set(key, pending);
	while (textCache.size > TEXT_CACHE_SIZE) textCache.delete(textCache.keys().next().value!);
	return pending;
}
