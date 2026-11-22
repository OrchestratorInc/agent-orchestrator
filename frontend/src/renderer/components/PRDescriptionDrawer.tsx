import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import { PRDescriptionBody } from "./PRDescriptionBody";
import { CloseButton } from "./ui/close-button";
import { Dialog, DialogClose, DialogContent, DialogOverlay, DialogTitle } from "./ui/dialog";

// About a third of a laptop window: room for a description's lines and tables
// while most of the chat stays in view.
const MAX_WIDTH = 440;
// Narrower than this beside the inspector, the drawer covers the inspector instead.
const MIN_WIDTH = 320;
const EDGE = 8;

type Placement = { top: number; bottom: number; right: number; width: number; dimRight: number };

/**
 * Where the drawer sits: against the inspector's left edge, so the card that
 * opened it stays in view, or over the inspector in a window too narrow for that.
 */
function useDrawerPlacement(open: boolean, anchor: RefObject<HTMLElement | null>): Placement {
	const [placement, setPlacement] = useState<Placement>({ top: EDGE, bottom: EDGE, right: EDGE, width: MAX_WIDTH, dimRight: 0 });
	useLayoutEffect(() => {
		if (!open) return;
		const panel = anchor.current?.closest<HTMLElement>("[data-slot='inspector-container']");
		// The inspector's tab bar sits in the window's title bar; the drawer starts under it.
		const bar = panel?.querySelector<HTMLElement>(".session-inspector__topbar");
		const measure = () => {
			const box = panel?.getBoundingClientRect();
			const top = Math.max((bar ?? panel)?.getBoundingClientRect()[bar ? "bottom" : "top"] ?? 0, 0) + EDGE / 2;
			const bottom = Math.max(window.innerHeight - (box?.bottom ?? window.innerHeight), 0) + EDGE / 2;
			const room = (box?.left ?? 0) - EDGE;
			setPlacement(box && room >= MIN_WIDTH
				? { top, bottom, right: window.innerWidth - box.left, width: Math.min(MAX_WIDTH, room), dimRight: window.innerWidth - box.left }
				: { top, bottom, right: EDGE, width: Math.min(MAX_WIDTH, window.innerWidth - 2 * EDGE), dimRight: 0 });
		};
		measure();
		window.addEventListener("resize", measure);
		const observer = typeof ResizeObserver === "undefined" || !panel ? undefined : new ResizeObserver(measure);
		if (panel) observer?.observe(panel);
		return () => {
			window.removeEventListener("resize", measure);
			observer?.disconnect();
		};
	}, [anchor, open]);
	return placement;
}

/**
 * A PR's whole description in a drawer beside the inspector, opened from the
 * PR card's title. The chat behind it dims; the inspector stays as it was.
 * Closes on ✕, Escape or a click outside, and gives focus back to the title.
 */
export function PRDescriptionDrawer({ anchor, cloud, detailsFailed, onOpenChange, open, pr, title }: {
	/** An element inside the inspector, to place the drawer beside it. */
	anchor: RefObject<HTMLElement | null>;
	cloud: boolean;
	detailsFailed?: boolean;
	onOpenChange: (open: boolean) => void;
	open: boolean;
	pr: SessionPRSummary;
	title: ReactNode;
}) {
	const { t } = useTranslation();
	const placement = useDrawerPlacement(open, anchor);
	// Radix gives focus back only to its own trigger, and the title is not one:
	// keep what had focus as the drawer opens (before Radix moves it in) and return it.
	const returnFocus = useRef<HTMLElement | null>(null);
	const bodyRef = useRef<HTMLDivElement>(null);
	useLayoutEffect(() => {
		if (open) returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
	}, [open]);
	return (
		<Dialog onOpenChange={onOpenChange} open={open}>
			<DialogContent
				aria-describedby={undefined}
				className={cn(
					// The app's own surface, as in the PR tab, lifted by a hairline edge and a shadow.
					"left-auto max-w-none translate-x-0 translate-y-0 gap-0 overflow-hidden border-border bg-(--color-bg-primary) p-0 shadow-xl sm:rounded-none",
					placement.dimRight ? "rounded-l-lg border-r-0 sm:rounded-l-lg" : "rounded-lg sm:rounded-lg",
					"duration-200 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:slide-in-from-right-4",
					"data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:slide-out-to-right-4",
				)}
				data-testid="pr-description-drawer"
				onCloseAutoFocus={(event) => {
					event.preventDefault();
					returnFocus.current?.focus();
				}}
				// Start on the text, not ✕, so the arrow keys, Space and Page Down scroll it.
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					bodyRef.current?.focus();
				}}
				overlay={<DialogOverlay style={{ right: placement.dimRight }} />}
				showCloseButton={false}
				style={{ top: placement.top, bottom: placement.bottom, right: placement.right, width: placement.width }}
			>
				<header className="flex h-11 shrink-0 items-center gap-2 border-b border-(--color-border-overlay) pr-2 pl-4">
					<DialogTitle className="min-w-0 flex-1 truncate text-sm font-medium">{title}</DialogTitle>
					<DialogClose asChild>
						<CloseButton aria-label={t("common.close")} size="icon-sm" />
					</DialogClose>
				</header>
				{/* A tab stop of its own, after ✕, so Tab moves between the two even when the text has no links. */}
				<div
					aria-label={t("pr.description.title")}
					className="min-h-0 flex-1 overflow-y-auto px-5 py-4 outline-none focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-inset"
					ref={bodyRef}
					role="region"
					tabIndex={0}
				>
					<PRDescriptionBody cloud={cloud} detailsFailed={detailsFailed} narrow pr={pr} />
				</div>
			</DialogContent>
		</Dialog>
	);
}
