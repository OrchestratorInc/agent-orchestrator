import { useId, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { AlignLeft, ArrowUpRight, ChevronRight } from "lucide-react";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prBrowserUrl } from "../lib/pr-display";
import { descriptionPreview } from "../lib/pr-description";
import { cn } from "../lib/utils";
import { ChatMarkdown } from "./chat/ChatMarkdown";
import { ProductExternalLink } from "./ProductExternalLink";

const toggleClass =
	"inline-flex shrink-0 items-center gap-0.5 rounded-sm text-xs text-markdown-link underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

/**
 * The PR body inside the PR card: two plain lines that expand in place to the
 * rendered markdown. It reads the body the daemon already stored, so opening
 * it makes no provider call.
 */
export function PullRequestDescription({ cloud, pr }: { cloud: boolean; pr: SessionPRSummary }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const [open, setOpen] = useState(false);
	const [overflows, setOverflows] = useState(false);
	const previewRef = useRef<HTMLParagraphElement>(null);
	const bodyId = useId();
	const description = pr.description?.trim() ?? "";
	const preview = description ? descriptionPreview(description) : "";
	useLayoutEffect(() => {
		const element = previewRef.current;
		if (!element || open) return;
		const measure = () => setOverflows(element.scrollHeight > element.clientHeight + 1);
		measure();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [open, preview]);

	let empty: string | undefined;
	if (!pr.detailsObservedAt) {
		// Only the local GitHub observer fetches details; elsewhere they never arrive.
		empty = pr.provider === "github" && !cloud ? t("pr.description.pending") : t("pr.description.unsupported");
	} else if (!preview) {
		empty = t("pr.description.none");
	}
	// Two lines that hold all of it, as written, need no toggle.
	const canToggle = !empty && (open || overflows || preview !== description);
	const toggle = () => setOpen((value) => !value);
	const fade = reduceMotion ? { duration: 0 } : { duration: 0.18, ease: [0.4, 0, 0.2, 1] as const };
	// The outgoing view leaves fast, so the swap reads as one motion.
	const leave = { opacity: 0, transition: reduceMotion ? { duration: 0 } : { duration: 0.08 } };

	return (
		<section
			className={cn(
				"group/description mt-2 flex min-w-0 flex-col rounded-md border border-border/60 bg-background px-2.5 py-2 transition-colors",
				canToggle && !open && "hover:border-border-strong",
			)}
			data-testid="pr-description"
		>
			<div className="flex min-w-0 items-center justify-between gap-2">
				<h4 className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
					<AlignLeft aria-hidden="true" className="size-icon-2xs shrink-0" />
					{t("pr.description.title")}
				</h4>
				{canToggle ? (
					<button aria-controls={bodyId} aria-expanded={open} className={toggleClass} onClick={toggle} type="button">
						{open ? t("inspector.showLess") : t("inspector.showMore")}
						<ChevronRight aria-hidden="true" className={cn("size-icon-sm transition-transform motion-reduce:transition-none", open && "rotate-90")} />
					</button>
				) : null}
			</div>
			<div className="mt-1 min-w-0 break-words" id={bodyId}>
				{empty ? (
					<p className="text-xs text-passive">{empty}</p>
				) : (
					<AnimatePresence initial={false} mode="wait">
						{open ? (
							<motion.div animate={{ opacity: 1, y: 0 }} exit={leave} initial={{ opacity: 0, y: -4 }} key="full" transition={fade}>
								{/* The PR body is provider content: ChatMarkdown renders markdown only, never raw HTML. */}
								<ChatMarkdown text={description} />
								{pr.descriptionTruncated ? <p className="mt-1.5 text-xs text-passive">{t("pr.description.truncated")}</p> : null}
								<div className="mt-2 flex min-w-0 items-center justify-between gap-2 border-t border-border/60 pt-2">
									<button className={toggleClass} onClick={toggle} type="button">
										{t("inspector.showLess")}
										<ChevronRight aria-hidden="true" className="size-icon-sm -rotate-90" />
									</button>
									<ProductExternalLink
										className="inline-flex min-w-0 items-center gap-1 rounded-sm text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
										href={prBrowserUrl(pr)}
									>
										<span className="truncate">{t("inspector.openPR", { number: pr.number })}</span>
										<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
									</ProductExternalLink>
								</div>
							</motion.div>
						) : (
							<motion.p
								animate={{ opacity: 1 }}
								className={cn("line-clamp-2 text-sm leading-normal text-muted-foreground", canToggle && "cursor-pointer")}
								exit={leave}
								initial={{ opacity: 0 }}
								key="preview"
								// The header button is the control; a click on the text is a shortcut to it.
								onClick={canToggle ? toggle : undefined}
								ref={previewRef}
								transition={fade}
							>
								{preview}
							</motion.p>
						)}
					</AnimatePresence>
				)}
			</div>
		</section>
	);
}
