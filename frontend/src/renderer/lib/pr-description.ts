import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { mapOutsideMarkdownCode } from "./markdown-code";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { isWebImageUrl } from "../components/PRDescriptionImage";
import type { DescriptionImage } from "../components/PRImageGallery";

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

export function descriptionMarkdown(pr: SessionPRSummary): string {
	const body = withoutRawHtml(pr.description ?? "").trim().replace(/\n{3,}/g, "\n\n");
	const withoutHeading = body.replace(/^#{1,6}[ \t]+(?:pr[ \t]+)?(?:summary|description)[ \t]*:?[ \t]*#*[ \t]*(\n+|$)/i, "").trimStart();
	return withoutHeading || body;
}

type MarkdownNode = { type: string; url?: string; alt?: string | null; identifier?: string; children?: MarkdownNode[] };
const markdownParser = unified().use(remarkParse).use(remarkGfm);

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
