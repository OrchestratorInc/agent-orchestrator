import { useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { AlignLeft, Images, PanelLeftOpen } from "lucide-react";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { descriptionMarkdown, descriptionMedia } from "../lib/pr-description";
import { openPRTab, usePRTabs } from "../lib/pr-tabs";
import { linksOnlyImages } from "./PRDescriptionImage";
import { PRImageGallery } from "./PRImageGallery";

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

// Kept outside the component: a new renderer each render would remount every
// image placeholder and close an open lightbox on the next re-render. The
// card's preview leaves images out: the line under it counts them.
const previewComponents: Components = {
	a: ({ node, href, children }) => linksOnlyImages(node) ? null : (
		<AppLink href={href} rel="noopener noreferrer" target="_blank">
			{children}
		</AppLink>
	),
	img: () => null,
};

const footerLinkClass = "ml-auto inline-flex items-center gap-1 font-medium transition-colors hover:text-foreground focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

/**
 * The PR body inside the PR card: three lines of its text, a line counting
 * its images (they open in a gallery), and Read full
 * description, which opens the whole body in the PR's tab (a local PR; a
 * Cloud one has no tab). It reads the body the daemon already stored, so none
 * of this calls the provider.
 */
export function PRDescriptionPreview({ cloud, detailsFailed, pr, sessionKey }: {
	cloud: boolean;
	/** The one read of a PR's missing details failed (see usePRDetailsLoad). */
	detailsFailed?: boolean;
	pr: SessionPRSummary;
	sessionKey: string;
}) {
	const { t } = useTranslation();
	const [overflows, setOverflows] = useState(false);
	const [imageIndex, setImageIndex] = useState<number | null>(null);
	const bodyRef = useRef<HTMLDivElement>(null);
	const bodyId = useId();
	const description = descriptionMarkdown(pr);
	const { images, tables } = useMemo(() => descriptionMedia(description), [description]);
	const isOpen = usePRTabs(sessionKey).active === pr.url;
	useLayoutEffect(() => {
		const element = bodyRef.current;
		if (!element) return;
		const measure = () => setOverflows(element.scrollHeight > element.clientHeight + 1);
		measure();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [description]);

	let empty: string | undefined;
	if (!pr.detailsObservedAt) {
		// Only the local GitHub observer fetches details; elsewhere they never arrive.
		empty = detailsFailed ? t("pr.description.unavailable") : pr.provider === "github" && !cloud ? t("pr.description.pending") : t("pr.description.unsupported");
	} else if (!description) {
		empty = t("pr.description.none");
	}
	// The full view is worth opening when the card shows only part of the body;
	// only a local PR has a tab to open it in.
	const hasMedia = !empty && images.length > 0;
	const hasMore = !cloud && Boolean(pr.url) && !empty && (overflows || images.length > 0 || tables > 0 || pr.descriptionTruncated === true);

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
				// Paragraphs that only held images render empty and take no room.
				<div className={cn(bodyClass, "line-clamp-3 max-h-[3lh] overflow-hidden [&_p:empty]:hidden", overflows && "[mask-image:linear-gradient(to_bottom,black_55%,transparent)]")} id={bodyId} ref={bodyRef}>
					{/* The PR body is provider content: react-markdown never renders raw HTML.
					    line-clamp only folds text; the height cap folds tables, lists and code too. */}
					<ReactMarkdown components={previewComponents} remarkPlugins={[remarkGfm]}>{description}</ReactMarkdown>
				</div>
			)}
			{hasMedia || hasMore ? (
				<div className="mt-1 flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1 text-micro text-muted-foreground">
					{hasMedia ? (
						<p className="m-0 inline-flex min-w-0 items-center gap-1 text-passive" data-testid="pr-description-media">
							<Images aria-hidden="true" className="size-icon-2xs shrink-0" />
							{images.length > 0 ? (
								<button className="font-medium text-muted-foreground underline-offset-2 transition-colors hover:text-foreground hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => setImageIndex(0)} type="button">
									{t("pr.description.images", { count: images.length })}
								</button>
							) : null}
						</p>
					) : null}
					{hasMore ? (
						<button aria-pressed={isOpen} className={footerLinkClass} data-testid="pr-description-read" onClick={() => openPRTab(sessionKey, pr.url)} type="button">
							<PanelLeftOpen aria-hidden="true" className="size-icon-2xs" />
							{t("pr.description.readFull")}
						</button>
					) : null}
				</div>
			) : null}
			<PRImageGallery images={images} index={imageIndex} onIndexChange={setImageIndex} />
		</article>
	);
}
