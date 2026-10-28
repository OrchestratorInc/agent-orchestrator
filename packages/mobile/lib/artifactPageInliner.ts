import { resolveArtifactAssetPath } from "./artifacts";

/**
 * Makes an HTML artifact self-contained, the way `ao render` does for a render
 * page (backend/internal/cli/render_images.go): the files it links beside it
 * in the artifact folder are read through the authenticated API and written
 * into the page. The page itself comes from the artifact-files route, with
 * AO's theme bootstrap already in it, as the desktop frames it.
 *
 * The daemon serves an artifact page in a CSP sandbox, so it has an opaque
 * origin, and Android's WebView then sends no `SameSite=Lax` preview cookie
 * with the page's own requests: its images, stylesheets and scripts all fail.
 * And on any platform, an image the agent named by its absolute path on the
 * machine never loads from a URL. A page loaded from memory, with everything
 * inlined, needs no cookie and has no origin to reach the daemon from.
 */

/** Reads one file of the artifact folder, by its folder-relative path. */
export type ArtifactAssetLoader = {
	/** The file as text, for a stylesheet or script. Undefined when it cannot be read. */
	text(path: string): Promise<string | undefined>;
	/** The file as a `data:` URI with its size in bytes, for an image. */
	dataUri(path: string): Promise<{ uri: string; bytes: number } | undefined>;
};

// The limits `ao render` holds a page to.
export const MAX_INLINED_IMAGE_BYTES = 10 << 20;
export const MAX_INLINED_PAGE_BYTES = 25 << 20;

export type InlinedPage = {
	html: string;
	/** References that stayed as written: missing, too large, or over the page budget. */
	skipped: string[];
};

const STYLESHEET = /<link\b[^>]*\brel\s*=\s*["']?stylesheet["']?[^>]*>/gi;
const SCRIPT = /<script\b([^>]*?)\bsrc\s*=\s*(["'])(.*?)\2([^>]*)>\s*<\/script\s*>/gi;
const IMAGE_ATTRIBUTE = /(<(?:img|source|input|video)\b[^>]*?\b(?:src|poster)\s*=\s*)(["'])(.*?)\2/gi;
const SRCSET = /\s\bsrcset\s*=\s*(["'])[^"']*\1/gi;
const CSS_URL = /url\(\s*(["']?)([^"')]+?)\1\s*\)/gi;
const HREF = /\bhref\s*=\s*(["'])(.*?)\1/i;
const STYLE_BLOCK = /(<style\b[^>]*>)([\s\S]*?)(<\/style\s*>)/gi;
const STYLE_ATTRIBUTE = /(\sstyle\s*=\s*)(["'])(.*?)\2/gi;

/**
 * A reference to a file of the artifact folder: relative to the file it is in,
 * or (as agents often write them, and as `ao render` accepts) an absolute
 * path into this session's artifact folder on the machine.
 */
export function resolveArtifactReference(fromPath: string, ref: string, sessionId: string): string | undefined {
	const trimmed = ref.trim().replace(/\\/g, "/");
	if (trimmed.startsWith("/") && !trimmed.startsWith("//") || /^[a-z]:\//i.test(trimmed)) {
		const marker = `/artifacts/${sessionId}/`;
		const at = trimmed.lastIndexOf(marker);
		return at < 0 ? undefined : resolveArtifactAssetPath("", trimmed.slice(at + marker.length));
	}
	return resolveArtifactAssetPath(fromPath, ref);
}

export async function inlineArtifactPage(html: string, pagePath: string, load: ArtifactAssetLoader, sessionId: string): Promise<InlinedPage> {
	const skipped: string[] = [];
	let budget = MAX_INLINED_PAGE_BYTES - html.length;
	const images = new Map<string, string | undefined>();

	const image = async (fromPath: string, ref: string): Promise<string | undefined> => {
		const path = resolveArtifactReference(fromPath, ref, sessionId);
		if (!path) return undefined;
		if (images.has(path)) return images.get(path);
		const loaded = await load.dataUri(path);
		const fits = loaded && loaded.bytes <= MAX_INLINED_IMAGE_BYTES && loaded.uri.length <= budget;
		if (fits) budget -= loaded.uri.length;
		else skipped.push(ref);
		images.set(path, fits ? loaded.uri : undefined);
		return fits ? loaded.uri : undefined;
	};

	// url(...) in CSS, resolved against the file the CSS came from.
	const inlineCssUrls = (css: string, fromPath: string) => replaceAsync(css, CSS_URL, async (match, _quote: string, ref: string) => {
		const uri = await image(fromPath, ref);
		// Unquoted: a base64 data URI has no quote, space or parenthesis, and a
		// quote would end a double-quoted style attribute early.
		return uri ? `url(${uri})` : match;
	});

	const text = async (ref: string): Promise<{ path: string; body: string } | undefined> => {
		const path = resolveArtifactReference(pagePath, ref, sessionId);
		if (!path) return undefined;
		const body = await load.text(path);
		if (body === undefined || body.length > budget) {
			skipped.push(ref);
			return undefined;
		}
		budget -= body.length;
		return { path, body };
	};

	// The page's own CSS first, against the page's folder; a linked stylesheet's
	// url(...) resolve against its own file once it is inlined below.
	let page = await replaceAsync(html, STYLE_BLOCK, async (_match, open: string, css: string, close: string) => `${open}${await inlineCssUrls(css, pagePath)}${close}`);
	page = await replaceAsync(page, STYLE_ATTRIBUTE, async (_match, prefix: string, quote: string, css: string) => `${prefix}${quote}${await inlineCssUrls(css, pagePath)}${quote}`);
	page = await replaceAsync(page, STYLESHEET, async (tag) => {
		const ref = HREF.exec(tag)?.[2];
		const sheet = ref ? await text(ref) : undefined;
		if (!sheet) return tag;
		return `<style>${escapeClosingTag(await inlineCssUrls(sheet.body, sheet.path), "style")}</style>`;
	});
	page = await replaceAsync(page, SCRIPT, async (tag, before: string, _quote: string, ref: string, after: string) => {
		const script = await text(ref);
		if (!script) return tag;
		return `<script${before}${after}>${escapeClosingTag(script.body, "script")}</script>`;
	});
	page = await replaceAsync(page, IMAGE_ATTRIBUTE, async (match, prefix: string, quote: string, ref: string) => {
		const uri = await image(pagePath, ref);
		return uri ? `${prefix}${quote}${uri}${quote}` : match;
	});
	// A srcset beside an inlined src would still name files the page cannot load.
	page = page.replace(SRCSET, "");
	return { html: page, skipped };
}

/** Keeps inlined text from closing the element it now sits in. */
function escapeClosingTag(body: string, tag: "style" | "script"): string {
	return body.replace(new RegExp(`</${tag}`, "gi"), `<\\/${tag}`);
}

async function replaceAsync(input: string, pattern: RegExp, replace: (match: string, ...groups: string[]) => Promise<string>): Promise<string> {
	const matches = [...input.matchAll(pattern)];
	if (!matches.length) return input;
	let out = "";
	let last = 0;
	for (const match of matches) {
		out += input.slice(last, match.index) + await replace(match[0], ...(match.slice(1) as string[]));
		last = (match.index ?? 0) + match[0].length;
	}
	return out + input.slice(last);
}
