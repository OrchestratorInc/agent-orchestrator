/**
 * Gallery card previews, as the desktop draws them (frontend ArtifactThumbnail):
 * a page laid out wider than the card and scaled down into it, so the card
 * shows the top of the real thing.
 */

/** The width an HTML card lays its page out at, in CSS pixels: a phone's, as the viewer shows it. */
export const THUMBNAIL_PAGE_WIDTH = 420;

/** How much of a text file a card reads and shows. */
export const THUMBNAIL_TEXT_CHARS = 4000;

/** Text files larger than this get no text preview. */
export const MAX_THUMBNAIL_TEXT_BYTES = 256 * 1024;

/** The zoom that fits a page laid out at THUMBNAIL_PAGE_WIDTH into a card `cardWidth` points wide. */
export function thumbnailZoom(cardWidth: number): number {
	return cardWidth > 0 ? cardWidth / THUMBNAIL_PAGE_WIDTH : 1;
}

/**
 * The page with a CSS zoom written in, so it lays out at THUMBNAIL_PAGE_WIDTH
 * and draws at the card's width. The rule goes inside <head> (or <html>), never
 * before the doctype, which would drop the page into quirks mode.
 */
export function thumbnailPageHtml(html: string, zoom: number): string {
	const rule = `<style>html{zoom:${Number(zoom.toFixed(4))}!important;overflow:hidden!important}</style>`;
	for (const tag of [/<head\b[^>]*>/i, /<html\b[^>]*>/i]) {
		const match = tag.exec(html);
		if (match) return html.slice(0, match.index + match[0].length) + rule + html.slice(match.index + match[0].length);
	}
	const doctype = /^\s*<!doctype[^>]*>/i.exec(html);
	return doctype ? doctype[0] + rule + html.slice(doctype[0].length) : rule + html;
}
