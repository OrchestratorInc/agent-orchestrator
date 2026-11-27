import { type CSSProperties, type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCenterPanelBox } from "../../hooks/useCenterPanelBox";
import { useHarnessUpdateNoticeRect } from "../../hooks/useHarnessUpdateNoticeClearance";
import { useSwipeToDismiss } from "../../hooks/useSwipeToDismiss";
import { LoaderCircle } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { Button } from "./button";
import { CloseButton } from "./close-button";
import { type ToastArt, ToastArtView, toneArt } from "./toast-art";

export type ToastTone = "info" | "success" | "error";

/**
 * One compact card on one row: a small picture of the data behind the message,
 * the title over one line of help, then the action and ×.
 */
export type ToastItem = {
	/** Stable across updates, so a toast that changes keeps its place and changes in place. */
	id: string;
	title: string;
	help?: ReactNode;
	tone?: ToastTone;
	/** Without one, the card draws a rail in its tone. */
	art?: ToastArt;
	/** While `busy` it shows a spinner and the label, and cannot be pressed. */
	action?: { label: string; run: () => void; busy?: boolean };
	/** Pressing anywhere on the card runs this, such as opening the session a notification is about. */
	onOpen?: () => void;
	dismissLabel?: string;
	onDismiss: () => void;
};

// The deck: the newest card in front, two more peeking below it, a little
// further while the pointer rests on it.
const PEEK_PX = 7;
const HOVER_PEEK_PX = 11;
const PEEK_SCALE = 0.05;
const MAX_PEEKING = 2;
const GAP_PX = 6;

/**
 * Every toast in one deck in the top-right corner of the center panel, under
 * the page's top bar (and under the agent update notice while it shows). At
 * rest only the newest card reads, with the next two as edges behind it.
 * - Pointing at the deck lets those edges peek out a little further.
 * - Clicking it fans every card out downward; leaving it folds them back.
 * - Closed, the deck is one thing: a swipe to the right (two fingers or a
 *   drag) clears every card. Open, a swipe or × clears just that card.
 * Anything being read (pointed at or open) does not time out.
 */
export function ToastStack({ items, label, onPausedChange, onDismissAll }: {
	items: ToastItem[];
	label: string;
	onPausedChange?: (paused: boolean) => void;
	onDismissAll: () => void;
}) {
	const panel = useCenterPanelBox();
	const notice = useHarnessUpdateNoticeRect();
	const [hovered, setHovered] = useState(false);
	const [expanded, setExpanded] = useState(false);
	const [heights, setHeights] = useState<Record<string, number>>({});
	const deckRef = useRef<HTMLElement>(null);
	const pressRef = useRef<{ x: number; y: number } | null>(null);
	// Newest first.
	const ordered = items.slice().reverse();
	const open = expanded && ordered.length > 1;
	const paused = hovered || open;
	const onPausedChangeRef = useRef(onPausedChange);
	onPausedChangeRef.current = onPausedChange;
	useEffect(() => onPausedChangeRef.current?.(paused), [paused]);
	// Closed, the deck swipes away as one; open, each card swipes on its own.
	useSwipeToDismiss(deckRef, onDismissAll, !open && ordered.length > 1, { fade: false });

	if (ordered.length === 0) return null;
	const area = panel ?? { left: 0, top: 0, right: document.documentElement.clientWidth, bottom: document.documentElement.clientHeight, radius: "0px", topInset: 0 };

	// Below the agent update notice when it shares the corner.
	const top = notice ? Math.max(0, Math.round(notice.bottom - area.top) + 8 - 12) : area.topInset;

	let offset = 0;
	const placed = ordered.map((item, index) => {
		const height = heights[item.id] ?? 48;
		const y = open ? offset : Math.min(index, MAX_PEEKING) * (hovered ? HOVER_PEEK_PX : PEEK_PX);
		offset += height + GAP_PX;
		return { item, index, y };
	});
	const frontHeight = heights[ordered[0].id] ?? 48;
	const deckHeight = open ? offset - GAP_PX : frontHeight + Math.min(ordered.length - 1, MAX_PEEKING) * (hovered ? HOVER_PEEK_PX : PEEK_PX);

	return (
		<div className="pointer-events-none fixed isolate z-[calc(var(--z-overlay)+1)] overflow-hidden"
			style={{ left: area.left, top: area.top, width: area.right - area.left, height: area.bottom - area.top, borderRadius: area.radius }}>
			<section ref={deckRef} role="region" aria-label={label} data-toast-stack="" data-expanded={open || undefined}
				data-browser-native-overlay="true"
				className="toast-stack pointer-events-auto absolute top-3 right-3 w-[min(360px,calc(100%-24px))] touch-pan-y select-none"
				style={{ WebkitAppRegion: "no-drag", translate: `0 ${top}px`, height: deckHeight } as CSSProperties}
				onPointerEnter={() => setHovered(true)}
				onPointerLeave={() => {
					setHovered(false);
					setExpanded(false);
				}}
				onBlur={(event) => {
					if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setExpanded(false);
				}}
				// Closed, a click (or Enter on the front card) opens the deck instead
				// of the card; × still clears the front card.
				onPointerDownCapture={(event) => {
					pressRef.current = { x: event.clientX, y: event.clientY };
				}}
				onClickCapture={(event) => {
					if (open || ordered.length < 2) return;
					// The click that ends a drag is a swipe, not a request to open.
					const press = pressRef.current;
					if (press && Math.hypot(event.clientX - press.x, event.clientY - press.y) > 6) return;
					if ((event.target as Element).closest("[data-toast-close]")) return;
					event.preventDefault();
					event.stopPropagation();
					setExpanded(true);
				}}>
				{placed.map(({ item, index, y }) => {
					const behind = !open && index > 0;
					const hidden = !open && index > MAX_PEEKING;
					const scale = behind ? 1 - Math.min(index, MAX_PEEKING) * PEEK_SCALE : 1;
					// Closed, every card behind takes the front card's height, so the edges peek out evenly.
					const height = behind ? frontHeight : heights[item.id];
					return (
						<ToastSlot key={item.id} item={item} front={index === 0} behind={behind} hidden={hidden} height={height} swipe={open || ordered.length === 1}
							style={{ zIndex: ordered.length - index, transform: `translate3d(0, ${y}px, 0) scale(${scale})` }}
							onHeight={(height) => setHeights((current) => (current[item.id] === height ? current : { ...current, [item.id]: height }))} />
					);
				})}
			</section>
		</div>
	);
}

function ToastSlot({ item, front, behind, hidden, height, swipe, style, onHeight }: {
	item: ToastItem;
	front: boolean;
	behind: boolean;
	hidden: boolean;
	height: number | undefined;
	swipe: boolean;
	style: CSSProperties;
	onHeight: (height: number) => void;
}) {
	const slotRef = useRef<HTMLDivElement>(null);
	const onHeightRef = useRef(onHeight);
	onHeightRef.current = onHeight;
	useLayoutEffect(() => {
		// The card's own height comes from its contents, not its box, which the
		// deck may have set to another card's height.
		const card = slotRef.current?.querySelector<HTMLElement>("[data-toast-card]");
		const body = card?.querySelector<HTMLElement>(".toast-body");
		if (!card || !body) return;
		const report = () => {
			const box = getComputedStyle(card);
			const chrome = ["paddingTop", "paddingBottom", "borderTopWidth", "borderBottomWidth"]
				.reduce((sum, key) => sum + (Number.parseFloat(box[key as "paddingTop"]) || 0), 0);
			onHeightRef.current(Math.round(body.offsetHeight + chrome));
		};
		const observer = new ResizeObserver(report);
		observer.observe(body);
		report();
		return () => observer.disconnect();
	}, []);
	return (
		// The slot carries the deck position; the card inside it carries the swipe.
		<div ref={slotRef} data-behind={behind || undefined} data-hidden={hidden || undefined}
			className={cn("toast-slot absolute inset-x-0 top-0 origin-top", behind && "pointer-events-none")}
			style={style} aria-hidden={behind || undefined} inert={behind || undefined}>
			<ToastCard item={item} muted={behind} front={front} height={height} swipe={swipe} />
		</div>
	);
}

function ToastCard({ item, muted, front, height, swipe }: { item: ToastItem; muted: boolean; front: boolean; height: number | undefined; swipe: boolean }) {
	const { t } = useTranslation();
	const ref = useRef<HTMLElement>(null);
	// Opaque all the way out, like the agent update notice: the card floats over
	// the chat, and the frame's clip is what hides it.
	const leave = useSwipeToDismiss(ref, item.onDismiss, swipe && !muted, { fade: false });
	const error = item.tone === "error";
	const action = item.action;
	return (
		<article ref={ref} role={error ? "alert" : "status"} aria-label={item.title} data-toast-card="" data-front={front || undefined}
			data-openable={item.onOpen ? "" : undefined}
			onClick={item.onOpen ? (event) => {
				if ((event.target as Element).closest("[data-toast-close], [data-toast-action]")) return;
				item.onOpen?.();
			} : undefined}
			className="toast-card toast-enter relative touch-pan-y select-none overflow-hidden border shadow-(--shadow-popover)"
			style={height === undefined ? undefined : { height }}>
			{/* Behind cards show only their edges, so their contents step back once the deck has closed. */}
			<div className="toast-body flex items-center gap-2.5">
				<ToastArtView art={item.art ?? toneArt(item.tone ?? "info")} />
				<div className="min-w-0 flex-1">
					{item.onOpen ? (
						// The keyboard opens the card through its title; the pointer passes
						// through to the card, so a click anywhere opens it and a drag that
						// starts on the title still swipes.
						<button type="button" onClick={item.onOpen} data-toast-open=""
							className="pointer-events-none block w-full truncate text-left text-control leading-4 font-medium outline-none">
							{item.title}
						</button>
					) : (
						<p className="truncate text-control leading-4 font-medium">{item.title}</p>
					)}
					{item.help ? (
						<p aria-live={error ? "assertive" : "polite"} className="mt-0.5 line-clamp-2 text-caption leading-4 text-muted-foreground"
							title={typeof item.help === "string" ? item.help : undefined}>{item.help}</p>
					) : null}
				</div>
				{action ? (
					<Button size="xs" variant="secondary" className="relative z-10" aria-busy={action.busy || undefined} disabled={action.busy}
						onClick={action.busy ? undefined : action.run} data-toast-action="">
						{action.busy ? <LoaderCircle className="size-3 animate-spin" aria-hidden="true" /> : null}
						{action.label}
					</Button>
				) : null}
				<CloseButton aria-label={item.dismissLabel ?? t("shortcut.dismissNotification")} className="relative z-10" data-toast-close="" onClick={leave} size="icon-xs" />
			</div>
		</article>
	);
}
