import { useId, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { AlignLeft, ArrowUpRight } from "lucide-react";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prBrowserUrl } from "../lib/pr-display";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { PRDescriptionImage } from "./PRDescriptionImage";
import { ProductExternalLink } from "./ProductExternalLink";

// The Reviews tab's review summary typography, so the two read as one family.
const bodyClass = cn(
	"min-w-0 select-text break-words text-xs leading-relaxed text-muted-foreground",
	"[&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:underline-offset-2",
	"[&_code]:rounded [&_code]:bg-muted/55 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-foreground",
	"[&_h1]:mt-2 [&_h1]:mb-1 [&_h1]:font-medium [&_h1]:text-foreground [&_h2]:mt-2 [&_h2]:mb-1 [&_h2]:font-medium [&_h2]:text-foreground",
	"[&_h3]:mt-2 [&_h3]:mb-1 [&_h3]:font-medium [&_h3]:text-foreground",
	"[&_li]:my-0.5 [&_ol]:my-1.5 [&_ol]:list-decimal [&_ol]:pl-4 [&_p]:my-1.5 [&_pre]:my-2",
	"[&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:border [&_pre]:border-border [&_pre]:bg-muted/35 [&_pre]:p-2",
	"[&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_strong]:text-foreground [&_table]:my-2 [&_table]:w-full",
	"[&_table]:border-collapse [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1",
	"[&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_th]:text-foreground",
	"[&_ul]:my-1.5 [&_ul]:list-disc [&_ul]:pl-4 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
);

/**
 * Raw HTML in a PR body would show as literal tags, since the renderer never
 * runs it. Template comments and script or style blocks go, a key like
 * <kbd>⌘K</kbd> becomes inline code, an <img> (what GitHub writes for a
 * pasted screenshot) becomes a markdown image, and other tags drop to their
 * text. Code spans and fenced blocks are left exactly as written.
 */
export function withoutRawHtml(markdown: string): string {
	return markdown
		.split(/(```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`)/)
		.map((part, index) => index % 2 === 1 ? part : part
			.replace(/<!--[\s\S]*?-->/g, "")
			.replace(/<(script|style)\b[\s\S]*?<\/\1\s*>/gi, "")
			.replace(/<kbd>([^<`]*)<\/kbd>/gi, "`$1`")
			.replace(/<img\b[^>]*>/gi, imageAsMarkdown)
			.replace(/<\/?[a-z][a-z0-9-]*(\s[^>]*)?\/?>/gi, ""))
		.join("");
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

// Kept outside the component: a new renderer each render would remount every
// image placeholder and close an open lightbox on the next re-render.
const markdownComponents: Components = {
	a: ({ node, href, children }) => {
		// A linked image ([![shot](src)](href)) is just the image: its
		// placeholder is a button, which cannot sit inside a link.
		const elements = node?.children.filter((child) => child.type === "element") ?? [];
		const onlyImages = elements.length > 0
			&& elements.every((child) => child.tagName === "img")
			&& (node?.children ?? []).every((child) => child.type === "element" || (child.type === "text" && !child.value.trim()));
		if (onlyImages) return <>{children}</>;
		return (
			<AppLink href={href} rel="noopener noreferrer" target="_blank">
				{children}
			</AppLink>
		);
	},
	img: ({ alt, src }) => <PRDescriptionImage alt={alt} src={typeof src === "string" ? src : undefined} />,
};

const footerLinkClass = "font-medium transition-colors hover:text-foreground focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

/**
 * The PR body inside the PR card, shaped like a review summary: a small
 * header, the rendered markdown clamped to four lines, and Show more under it.
 * It reads the body the daemon already stored, so opening it makes no
 * provider call.
 */
export function PullRequestDescription({ cloud, pr }: { cloud: boolean; pr: SessionPRSummary }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [overflows, setOverflows] = useState(false);
	const bodyRef = useRef<HTMLDivElement>(null);
	const bodyId = useId();
	// Template padding (runs of blank lines) would only spend the four lines, and
	// an opening heading ("## Summary") only repeats the card's Description title.
	const body = withoutRawHtml(pr.description ?? "").trim().replace(/\n{3,}/g, "\n\n");
	const withoutHeading = body.replace(/^#{1,6}[ \t]+[^\n]*(\n+|$)/, "").trimStart();
	const description = withoutHeading || body;
	useLayoutEffect(() => {
		const element = bodyRef.current;
		if (!element || open) return;
		const measure = () => setOverflows(element.scrollHeight > element.clientHeight + 1);
		measure();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [open, description]);

	let empty: string | undefined;
	if (!pr.detailsObservedAt) {
		// Only the local GitHub observer fetches details; elsewhere they never arrive.
		empty = pr.provider === "github" && !cloud ? t("pr.description.pending") : t("pr.description.unsupported");
	} else if (!description) {
		empty = t("pr.description.none");
	}
	// A shortened body opens too, to say so and point at the full one.
	const canToggle = !empty && (open || overflows || pr.descriptionTruncated === true);

	return (
		// The PR card is already a filled surface, so the description keeps its own
		// edge and background to stand apart from it; the type inside matches a review summary.
		<article className="mt-2 flex min-w-0 flex-col gap-1 rounded-md border border-border/60 bg-background px-2.5 py-2.5" data-testid="pr-description">
			<h4 className="inline-flex min-w-0 items-center gap-1 text-micro font-medium text-muted-foreground">
				<AlignLeft aria-hidden="true" className="size-icon-2xs shrink-0" />
				{t("pr.description.title")}
			</h4>
			{empty ? (
				<p className="text-xs leading-relaxed text-passive">{empty}</p>
			) : (
				<div className={cn(bodyClass, !open && "line-clamp-4")} id={bodyId} ref={bodyRef}>
					{/* The PR body is provider content: react-markdown never renders raw HTML. */}
					<ReactMarkdown
						components={markdownComponents}
						remarkPlugins={[remarkGfm]}
					>
						{description}
					</ReactMarkdown>
				</div>
			)}
			{open && pr.descriptionTruncated ? <p className="text-micro text-passive">{t("pr.description.truncated")}</p> : null}
			{canToggle ? (
				<span className="mt-1 flex min-w-0 items-center justify-between gap-2 text-micro text-passive">
					<button aria-controls={bodyId} aria-expanded={open} className={footerLinkClass} onClick={() => setOpen((value) => !value)} type="button">
						{open ? t("inspector.showLess") : t("inspector.showMore")}
					</button>
					{open ? (
						<ProductExternalLink className={cn(footerLinkClass, "inline-flex min-w-0 items-center gap-0.5")} href={prBrowserUrl(pr)}>
							<span className="truncate">{t("inspector.openPR", { number: pr.number })}</span>
							<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
						</ProductExternalLink>
					) : null}
				</span>
			) : null}
		</article>
	);
}
