import { ImageIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { isWebImageUrl, linksOnlyImages, PRDescriptionImage } from "./PRDescriptionImage";
import { PRImageGallery } from "./PRImageGallery";
import { descriptionMarkdown, descriptionMedia } from "../lib/pr-description";

// Reading width and type for the whole body.
const readingClass = cn(
	"min-w-0 select-text break-words text-sm leading-relaxed text-foreground/85",
	"[&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:underline-offset-2",
	"[&_code]:rounded [&_code]:bg-muted/60 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.9em] [&_code]:text-foreground",
	"[&_h1]:mt-6 [&_h1]:mb-2 [&_h1]:text-lg [&_h1]:font-semibold [&_h1]:text-foreground",
	"[&_h2]:mt-6 [&_h2]:mb-2 [&_h2]:text-base [&_h2]:font-semibold [&_h2]:text-foreground",
	"[&_h3]:mt-4 [&_h3]:mb-1.5 [&_h3]:font-semibold [&_h3]:text-foreground",
	"[&_li]:my-1 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-2.5 [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5",
	"[&_pre]:my-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:border [&_pre]:border-border [&_pre]:bg-muted/40 [&_pre]:p-3 [&_pre_code]:bg-transparent [&_pre_code]:p-0",
	"[&_strong]:text-foreground [&_table]:my-3 [&_table]:w-full [&_table]:border-collapse",
	"[&_td]:border [&_td]:border-border [&_td]:p-2 [&_td]:align-top [&_th]:border [&_th]:border-border [&_th]:p-2 [&_th]:text-left [&_th]:font-medium [&_th]:text-foreground",
	"[&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
);

/**
 * An image in the body at its own size, opening the gallery. One that can't
 * load shrinks to its name; the gallery says why and links to the original.
 */
function InlineImage({ alt, onOpen, src }: { alt?: string; onOpen: () => void; src: string }) {
	const { t } = useTranslation();
	const [failed, setFailed] = useState(false);
	const name = alt?.trim() || t("pr.description.image");
	return (
		<button aria-label={t("pr.description.openImage", { name })} className={cn("my-1 max-w-full rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60", failed ? "inline-flex items-center gap-1 border border-border bg-muted/40 px-1.5 py-0.5 align-middle text-xs text-muted-foreground hover:text-foreground" : "inline-block")} onClick={onOpen} type="button">
			{failed ? <><ImageIcon aria-hidden="true" className="size-icon-2xs shrink-0" />{name}</> : <img alt={alt ?? ""} className="max-w-full rounded-md border border-border" onError={() => setFailed(true)} src={src} />}
		</button>
	);
}

/**
 * A PR's whole description, in its tab: the body at a readable width, images
 * full size (each opens the gallery). Says why when there is no body to show.
 */
export function PRDescriptionBody({ cloud, detailsFailed, pr }: {
	cloud: boolean;
	/** The one read of a PR's missing details failed (see usePRDetailsLoad). */
	detailsFailed?: boolean;
	pr: SessionPRSummary;
}) {
	const { t } = useTranslation();
	const [imageIndex, setImageIndex] = useState<number | null>(null);
	const markdown = descriptionMarkdown(pr);
	const { images } = useMemo(() => descriptionMedia(markdown), [markdown]);
	const components = useMemo<Components>(() => ({
		a: ({ node, href, children }) => linksOnlyImages(node) ? <>{children}</> : <AppLink href={href} rel="noopener noreferrer" target="_blank">{children}</AppLink>,
		img: ({ alt, src }) => {
			const url = typeof src === "string" ? src : undefined;
			const index = images.findIndex((image) => image.src === url);
			// A non-web source, or one the gallery doesn't hold, stays a label.
			if (!isWebImageUrl(url) || index < 0) return <PRDescriptionImage alt={alt} src={url} />;
			return <InlineImage alt={alt} onOpen={() => setImageIndex(index)} src={url} />;
		},
	}), [images]);

	let empty: string | undefined;
	if (!pr.detailsObservedAt) {
		// Only the local GitHub observer fetches details; elsewhere they never arrive.
		empty = detailsFailed ? t("pr.description.unavailable") : pr.provider === "github" && !cloud ? t("pr.description.pending") : t("pr.description.unsupported");
	} else if (!markdown) {
		empty = t("pr.description.none");
	}

	return (
		<div data-testid="pr-description">
			{empty ? <p className="m-0 text-sm text-passive">{empty}</p> : (
				<div className={readingClass}>
					{/* Provider content: react-markdown never renders raw HTML. */}
					<ReactMarkdown components={components} remarkPlugins={[remarkGfm]}>{markdown}</ReactMarkdown>
				</div>
			)}
			{pr.descriptionTruncated ? <p className="mt-4 text-xs text-passive">{t("pr.description.truncated")}</p> : null}
			<PRImageGallery images={images} index={imageIndex} onIndexChange={setImageIndex} />
		</div>
	);
}
