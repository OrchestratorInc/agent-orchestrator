import { escapeHtml } from "@pierre/words/html";
import { findSessionLinks } from "./session-links";

const NONCE = crypto.randomUUID().slice(0, 8);
export const LINK_ISLAND = `ao-link-${NONCE}`;
export const CODE_ISLAND = `ao-code-${NONCE}`;
export const IMAGE_ISLAND = `ao-image-${NONCE}`;

export const EMOJI_GRAPHEME = /\p{Extended_Pictographic}|\p{Regional_Indicator}|[#*0-9]️?⃣/u;
export const GRAPHEME_SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

const UNESCAPES: Record<string, string> = { "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#39;": "'", "&amp;": "&" };

export const unescapeHtml = (html: string) => html.replace(/&(?:lt|gt|quot|#39|amp);/g, (entity) => UNESCAPES[entity]!);

export const islandHref = (url: string) => `#${encodeURIComponent(url)}`;

const decodeIslandHref = (escaped: string) => {
	try {
		return decodeURIComponent(unescapeHtml(escaped));
	} catch {
		return undefined;
	}
};

function islandHost(name: string, value: string, data: Record<string, string>, fallback: string): string {
	const key = escapeHtml(JSON.stringify([name, value]));
	const json = escapeHtml(JSON.stringify({ ...data, id: name }));
	return `<span data-words-island="${name}" data-words-island-id="${key}" data-words-island-data="${json}"><span data-words-island-content></span><span data-words-island-fallback>${fallback}</span></span>`;
}

const IMAGE_HOST = new RegExp(`^<span data-words-island="${IMAGE_ISLAND}" data-words-island-id="[^"]*" data-words-island-data="([^"]*)"><span data-words-island-content></span><span data-words-island-fallback>[^<]*</span></span>$`);

export function linkIsland(html: string): string {
	const open = /^<a data-markdown="a" href="#([^"]*)"[^>]*>/.exec(html);
	if (!open || !html.endsWith("</a>")) return html;
	const href = decodeIslandHref(open[1]!);
	if (href === undefined) return html;
	const label = html.slice(open[0].length, -"</a>".length);
	const image = IMAGE_HOST.exec(label)?.[1];
	const { src, alt } = image === undefined ? {} : JSON.parse(unescapeHtml(image)) as { src?: string; alt?: string };
	return islandHost(LINK_ISLAND, href, src === undefined ? { href, label } : { href, label, src, alt: alt ?? "" }, label);
}

export function imageIsland(html: string): string {
	const escaped = /^<img data-markdown="img" src="#([^"]*)"/.exec(html)?.[1];
	const src = escaped === undefined ? undefined : decodeIslandHref(escaped);
	if (src === undefined) return html;
	const alt = / alt="([^"]*)"/.exec(html)?.[1] ?? "";
	return islandHost(IMAGE_ISLAND, src, { src, alt: unescapeHtml(alt) }, alt);
}

export function codeIsland(html: string): string {
	const text = /^<code data-markdown="code">([^<]*)<\/code>$/.exec(html)?.[1];
	if (text === undefined) return html;
	const code = unescapeHtml(text);
	return islandHost(CODE_ISLAND, code, { text: code }, html);
}

function emojiSpans(text: string): string {
	if (!EMOJI_GRAPHEME.test(text)) return escapeHtml(text);
	let html = "";
	for (const { segment } of GRAPHEME_SEGMENTER.segment(text)) {
		html += EMOJI_GRAPHEME.test(segment) ? `<span class="chat-md-emoji">${segment}</span>` : escapeHtml(segment);
	}
	return html;
}

const OPAQUE = /^<(?:a|code|pre|svg)[\s>]| data-words-island="/;
const VOID = /^<(?:br|hr|img|input)[\s>]|\/>$/;

function proseHtml(html: string, emoji: boolean): string {
	if (!html.includes("ao://") && !(emoji && EMOJI_GRAPHEME.test(html))) return html;
	const opaque: boolean[] = [];
	return html.split(/(<[^>]*>)/).map((part, index) => {
		if (index % 2 === 1) {
			if (part.startsWith("</")) opaque.pop();
			else if (!VOID.test(part)) opaque.push(opaque.at(-1) === true || OPAQUE.test(part));
			return part;
		}
		const links = part.includes("ao://");
		const direct = emoji && opaque.length === 1 && EMOJI_GRAPHEME.test(part);
		if (opaque.at(-1) || (!links && !direct)) return part;
		const text = unescapeHtml(part);
		const prose = direct ? emojiSpans : escapeHtml;
		let out = "";
		let cursor = 0;
		for (const link of links ? findSessionLinks(text) : []) {
			const label = escapeHtml(link.text);
			out += prose(text.slice(cursor, link.start)) + islandHost(LINK_ISLAND, link.text, { href: link.text, label }, label);
			cursor = link.end;
		}
		return out + prose(text.slice(cursor));
	}).join("");
}

export const proseIslands = (html: string) => proseHtml(html, true);
export const headingIslands = (html: string) => proseHtml(html, false);
