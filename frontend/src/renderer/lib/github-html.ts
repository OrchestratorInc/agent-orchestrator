import type { Options } from "react-markdown";
import rehypeRaw from "rehype-raw";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";

/**
 * The HTML GitHub itself allows in comments, as GitHub renders it: bots like
 * CodeRabbit write `<details>` sections, `<dl>` indents and badge images, and
 * those read as their structure rather than as literal tags. rehype-sanitize's
 * default schema is GitHub's own allowlist; it drops scripts, styles, event
 * handlers and HTML comments (a bot's hidden state). `<picture>`/`<source>`
 * unwrap to the `<img>` inside them, so the light-mode image is the one kept.
 */
const schema = {
	...defaultSchema,
	tagNames: defaultSchema.tagNames?.filter((tag) => tag !== "picture" && tag !== "source"),
	protocols: { ...defaultSchema.protocols, href: [...(defaultSchema.protocols?.href ?? []), "ao"] },
};

/** For PR comments and reviews: images keep their source, size and text, for the image renderer to decide on. */
export const GITHUB_HTML_REHYPE: Options["rehypePlugins"] = [rehypeRaw, [rehypeSanitize, {
	...schema,
	attributes: { ...schema.attributes, img: ["src", "alt", "width", "height"] },
}]];

/** For chat, which never loads remote images: an `<img>` keeps no attributes. */
export const GITHUB_HTML_REHYPE_NO_IMAGES: Options["rehypePlugins"] = [rehypeRaw, [rehypeSanitize, {
	...schema,
	attributes: { ...schema.attributes, img: [], code: [["className", /^language-./, "math-inline", "math-display"]] },
}]];

/**
 * Whether an image is an inline icon (CodeRabbit's 12–16px glyphs beside a
 * heading) rather than content: no alt text, and every size it is given is icon-sized. Such an
 * image adds nothing to read, so it is left out instead of shown as an
 * "Image" chip in the middle of a sentence.
 */
export function isDecorativeIcon(alt: string | undefined, width: unknown, height: unknown): boolean {
	if (alt?.trim()) return false;
	const size = (value: unknown) => {
		const parsed = typeof value === "number" ? value : Number.parseInt(String(value ?? ""), 10);
		return Number.isFinite(parsed) ? parsed : undefined;
	};
	// Every size it gives is icon-sized: a 600×32 banner is content, not a glyph.
	const sizes = [size(width), size(height)].filter((value): value is number => value !== undefined);
	return sizes.length > 0 && sizes.every((value) => value <= 32);
}
