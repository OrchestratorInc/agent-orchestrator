import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { mapOutsideMarkdownCode } from "./markdown-code";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { isWebImageUrl } from "../components/PRDescriptionImage";
import type { DescriptionImage } from "../components/PRImageGallery";

/**
 * Raw HTML in a PR body would show as literal tags, since the renderer never
 * runs it. Template comments and script or style blocks go, a key like
 * <kbd>⌘K</kbd> becomes inline code, an <img> (what GitHub writes for a
 * pasted screenshot) becomes a markdown image, and other tags drop to their
 * text. Code spans and fenced blocks are left exactly as written.
 */
export function withoutRawHtml(markdown: string): string {
	return mapOutsideMarkdownCode(markdown, (prose) => prose
		.replace(/<!--[\s\S]*?-->/g, "")
		.replace(/<(script|style)\b[\s\S]*?<\/\1\s*>/gi, "")
		.replace(/<kbd>([^<`]*)<\/kbd>/gi, "`$1`")
		.replace(/<img\b[^>]*>/gi, imageAsMarkdown)
		.replace(/<\/?[a-z][a-z0-9-]*(\s[^>]*)?\/?>/gi, ""));
}

function htmlAttribute(tag: string, name: string): string | undefined {
	const match = new RegExp(`\\s${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^\\s>]+))`, "i").exec(tag);
	return match ? (match[1] ?? match[2] ?? match[3]) : undefined;
}

function imageAsMarkdown(tag: string): string {
	const src = htmlAttribute(tag, "src")?.trim().replace(/&amp;/g, "&");
	if (!src) return "";
	const alt = (htmlAttribute(tag, "alt") ?? "").replace(/[[\]\\]/g, "");
	return `![${alt}](<${src.replace(/[<>\s]/g, encodeURIComponent)}>)`;
}

/**
 * The PR body as its tab shows it: raw HTML made safe, template padding
 * squeezed, and an opening heading dropped when it only repeats the
 * Description title ("## Summary").
 */
export function descriptionMarkdown(pr: SessionPRSummary): string {
	const body = withoutRawHtml(pr.description ?? "").trim().replace(/\n{3,}/g, "\n\n");
	const withoutHeading = body.replace(/^#{1,6}[ \t]+(?:pr[ \t]+)?(?:summary|description)[ \t]*:?[ \t]*#*[ \t]*(\n+|$)/i, "").trimStart();
	return withoutHeading || body;
}

type MarkdownNode = { type: string; url?: string; alt?: string | null; identifier?: string; children?: MarkdownNode[] };
const markdownParser = unified().use(remarkParse).use(remarkGfm);

/**
 * The description's web images, in order, and how many tables it holds, read
 * from the same markdown tree the renderer draws: code never counts, and an
 * image's URL is exactly the one the renderer loads (titles, parentheses,
 * escapes and reference images included). Other images stay labels in the text.
 */
export function descriptionMedia(markdown: string): { images: DescriptionImage[]; tables: number } {
	const tree = markdownParser.parse(markdown) as MarkdownNode;
	const definitions = new Map<string, string>();
	const nodes: MarkdownNode[] = [];
	const walk = (node: MarkdownNode) => {
		nodes.push(node);
		if (node.type === "definition" && node.identifier && node.url) definitions.set(node.identifier, node.url);
		node.children?.forEach(walk);
	};
	walk(tree);
	const images: DescriptionImage[] = [];
	let tables = 0;
	for (const node of nodes) {
		if (node.type === "table") tables += 1;
		const src = node.type === "image" ? node.url : node.type === "imageReference" && node.identifier ? definitions.get(node.identifier) : undefined;
		if (isWebImageUrl(src)) images.push({ alt: node.alt ?? "", src });
	}
	return { images, tables };
}

type FooterNode = { type: string; value?: string; children?: FooterNode[]; data?: { hProperties?: Record<string, unknown> } };

// A sign-off is a line or two and a link, never a section.
const FOOTER_MAX_BLOCKS = 3;
const FOOTER_MAX_TEXT = 280;

const nodeText = (node: FooterNode): string => node.value ?? (node.children ?? []).map(nodeText).join("");

/**
 * A remark plugin for a description's sign-off, such as "Generated with …" and
 * a session link under a closing rule: the paragraphs after the last rule get
 * the class `pr-description-footer`, so they read small and quiet instead of
 * as body text. Only a short run of plain paragraphs counts; a heading, list,
 * table or code block after the rule is a section and stays as it is.
 */
export function remarkQuietFooter() {
	return (tree: FooterNode) => {
		const blocks = tree.children ?? [];
		let rule = -1;
		for (let index = blocks.length - 1; index >= 0; index -= 1) {
			if (blocks[index]?.type === "thematicBreak") {
				rule = index;
				break;
			}
		}
		if (rule < 0) return;
		const tail = blocks.slice(rule + 1);
		if (tail.length === 0 || tail.length > FOOTER_MAX_BLOCKS) return;
		if (tail.some((node) => node.type !== "paragraph")) return;
		if (tail.map(nodeText).join("").length > FOOTER_MAX_TEXT) return;
		for (const node of tail) node.data = { ...node.data, hProperties: { ...node.data?.hProperties, className: ["pr-description-footer"] } };
	};
}
