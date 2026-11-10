import { Slot } from "@radix-ui/react-slot";
import { createContext, type CSSProperties, type ComponentPropsWithoutRef, type ReactNode, useContext, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { publishHarnessUpdateNoticeRect } from "../hooks/useHarnessUpdateNoticeClearance";
import { useCenterPanelBox } from "../hooks/useCenterPanelBox";
import { useLiveBrowserViewportRect, type ViewportRect } from "../hooks/useLiveBrowserViewportRect";
import { useSwipeToDismiss } from "../hooks/useSwipeToDismiss";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";

/*
 * The floating notice in the center panel's top-right corner (agent updates,
 * the chat import offer). Compose a card from the parts:
 *
 *   <NoticeSlot id="…" label="…">
 *     <Notice label="…" onDismiss={…}>
 *       <NoticeHeader>
 *         <NoticeIcon>…</NoticeIcon>
 *         <NoticeTitle>…</NoticeTitle>
 *         <NoticeAdornment>…</NoticeAdornment>   optional, before the ×
 *         <NoticeDismiss label="…" />
 *       </NoticeHeader>
 *       <NoticeDescription>…</NoticeDescription>
 *       <NoticeActions>…main action last, bottom right…</NoticeActions>
 *     </Notice>
 *   </NoticeSlot>
 *
 * Swiping the card right, the ×, and NoticeDismissAction all run the same
 * leave animation before `onDismiss`. `NoticeViewport` is mounted once in the
 * shell; every NoticeSlot stacks into its frame.
 */

const NoticeDismissContext = createContext<(() => void) | null>(null);

function useNoticeDismiss(): () => void {
	const dismiss = useContext(NoticeDismissContext);
	if (!dismiss) throw new Error("Notice parts must render inside <Notice>.");
	return dismiss;
}

/** A notice card, or a row inside one (`variant="row"`): swipe it right (two
 * fingers or a drag) or press × to dismiss it. */
export function Notice({ variant = "card", role, label, onDismiss, swipeable = true, children }: {
	variant?: "card" | "row";
	role?: "status" | "group";
	label: string;
	onDismiss: () => void;
	swipeable?: boolean;
	children: ReactNode;
}) {
	const ref = useRef<HTMLElement>(null);
	const leave = useSwipeToDismiss(ref, onDismiss, swipeable);
	return <NoticeDismissContext.Provider value={leave}>
		<article ref={ref} aria-label={label} role={role}
			// The chat model selector's menu surface, so it reads as a floating menu
			// apart from the composer and other cards it can sit over.
			className={cn("relative touch-pan-y select-none px-3 py-2.5",
				variant === "card" && "rounded-(--radius-settings-panel) border border-(--color-border-settings-menu) bg-settings-menu shadow-(--shadow-popover)")}>
			{children}
		</article>
	</NoticeDismissContext.Provider>;
}

/** Icon, title, optional adornment and × on one line. */
export function NoticeHeader({ children }: { children: ReactNode }) {
	return <div className="flex items-center gap-2">{children}</div>;
}

/** The leading mark. Styles its single child, adding no wrapper. */
export function NoticeIcon({ children }: { children: ReactNode }) {
	return <Slot className="shrink-0">{children}</Slot>;
}

export function NoticeTitle({ children }: { children: ReactNode }) {
	return <p className="min-w-0 flex-1 truncate text-control font-medium">{children}</p>;
}

/** Trailing marks between the title and the ×, such as agent logos. */
export function NoticeAdornment({ className, ...props }: ComponentPropsWithoutRef<"span">) {
	return <span {...props} className={cn("flex shrink-0 items-center text-caption text-muted-foreground", className)} />;
}

/** Overlapping agent marks, each cut out of the one beneath by a halo in the card's colour. */
export const NOTICE_MARK_HALO = "[filter:drop-shadow(0_0_1px_var(--color-bg-settings-menu))_drop-shadow(0_0_1px_var(--color-bg-settings-menu))_drop-shadow(0_0_2px_var(--color-bg-settings-menu))_drop-shadow(0_0_3px_var(--color-bg-settings-menu))]";

/** The × in the header. */
export function NoticeDismiss({ label }: { label: string }) {
	const dismiss = useNoticeDismiss();
	return <CloseButton aria-label={label} className="-mr-1" onClick={dismiss} size="icon-xs" />;
}

/** One line under the title. Polite live region, so a change to it is read out. */
export function NoticeDescription({ children }: { children: ReactNode }) {
	return <p aria-live="polite" className="mt-0.5 text-caption text-muted-foreground">{children}</p>;
}

/** The action row. Put the main action last so it lands bottom right on every card. */
export function NoticeActions({ children }: { children: ReactNode }) {
	return <div className="mt-2 flex items-center gap-2">{children}</div>;
}

/** A text action ("Not now") that leaves the way the × does. */
export function NoticeDismissAction({ children }: { children: ReactNode }) {
	const dismiss = useNoticeDismiss();
	return <Button type="button" size="sm" variant="secondary" className="shrink-0" onClick={dismiss}>{children}</Button>;
}

// Notices register here. NoticeViewport draws one host per notice, sorted by
// `order`, and each notice portals into its own host, so the stack's DOM, focus
// and reading order match what is on screen.
type SlotEntry = { id: string; label: string; order: number };
let slotEntries: readonly SlotEntry[] = [];
const slotHosts = new Map<string, HTMLElement>();
const slotListeners = new Set<() => void>();
function emitSlots() {
	for (const listener of slotListeners) listener();
}
function subscribeSlots(listener: () => void) {
	slotListeners.add(listener);
	return () => { slotListeners.delete(listener); };
}
const readSlots = () => slotEntries;
const hostRefs = new Map<string, (element: HTMLDivElement | null) => void>();
function hostRef(id: string) {
	let ref = hostRefs.get(id);
	if (!ref) {
		ref = (element) => {
			if (element) slotHosts.set(id, element);
			else if (slotHosts.has(id)) slotHosts.delete(id);
			else return;
			emitSlots();
		};
		hostRefs.set(id, ref);
	}
	return ref;
}

/** Puts its notice in the corner frame while mounted. Lower `order` sits higher. */
export function NoticeSlot({ id, label, order = 0, children }: { id: string; label: string; order?: number; children: ReactNode }) {
	useLayoutEffect(() => {
		slotEntries = [...slotEntries.filter((entry) => entry.id !== id), { id, label, order }]
			.sort((left, right) => left.order - right.order);
		emitSlots();
		return () => {
			slotEntries = slotEntries.filter((entry) => entry.id !== id);
			emitSlots();
		};
	}, [id, label, order]);
	const host = useSyncExternalStore(subscribeSlots, () => slotHosts.get(id) ?? null, () => null);
	return host ? createPortal(children, host) : null;
}

/** Mounted once in the shell; draws the corner frame while any notice is shown. */
export function NoticeViewport() {
	const { t } = useTranslation();
	const slots = useSyncExternalStore(subscribeSlots, readSlots, readSlots);
	// Settings is a modal that blocks pointer and focus outside it.
	const settingsOpen = useUiStore((state) => state.settingsModal !== null);
	if (slots.length === 0 || settingsOpen) return null;
	// Portaled to the end of <body>: Electron applies window drag regions in DOM
	// order, so a later drag strip (the home page's) would otherwise override no-drag.
	return createPortal(<NoticeFrame label={slots.length === 1 ? slots[0]!.label : t("notify.title")}>
		<div className="flex flex-col gap-2">
			{slots.map((slot) => <div key={slot.id} ref={hostRef(slot.id)} className="toast-enter" />)}
		</div>
	</NoticeFrame>, document.body);
}

// Longer than the list's, a closing row's and the top-bar slide's transitions (200ms, 180ms, 200ms).
const MOTION_SETTLE_MS = 260;

/** The notice's frame in the top-right corner of the framed center panel, just
 * below the page's top bar when it has one, so it never covers a page's own
 * actions and slides, rather than jumps, between pages. A fixed box that
 * tracks the panel clips it, so a swipe slides it out under the panel's edge
 * instead of over the window frame.
 *
 * The live browser page is a native view: anything drawn over it has to raise
 * the whole transparent shell above the page, which then swallows every click
 * and scroll meant for the browser. This notice never does that. It turns
 * invisible while the live page overlaps it, and stays measurable meanwhile. */
function NoticeFrame({ label, children }: { label: string; children: ReactNode }) {
	const browserRect = useLiveBrowserViewportRect();
	const panel = useCenterPanelBox();
	const frameRef = useRef<HTMLElement>(null);
	const [box, setBox] = useState<ViewportRect | null>(null);
	useLayoutEffect(() => {
		const element = frameRef.current;
		if (!element) return;
		// While a height or position transition runs (the list opening, a row
		// closing, the notice following a top bar) the rect changes every frame;
		// measuring then would re-render every reader of the rect each frame, so
		// it measures once the motion has settled.
		let settle: number | undefined;
		const measure = () => {
			if (settle !== undefined) return;
			const rect = element.getBoundingClientRect();
			const next = rect.width > 0 ? { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom } : null;
			setBox((current) => (current && next && current.left === next.left && current.top === next.top && current.right === next.right && current.bottom === next.bottom ? current : next));
		};
		const onTransition = (event: TransitionEvent) => {
			if (event.propertyName !== "height" && event.propertyName !== "translate") return;
			window.clearTimeout(settle);
			settle = window.setTimeout(() => {
				settle = undefined;
				measure();
			}, MOTION_SETTLE_MS);
		};
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		window.addEventListener("resize", measure);
		element.addEventListener("transitionrun", onTransition);
		measure();
		return () => {
			window.clearTimeout(settle);
			observer.disconnect();
			window.removeEventListener("resize", measure);
			element.removeEventListener("transitionrun", onTransition);
		};
	}, [panel]);
	const covered = noticeCoversLiveBrowser(browserRect, box);
	// Session content in this corner reads this rect to keep itself clear.
	useLayoutEffect(() => publishHarnessUpdateNoticeRect(covered ? null : box), [box, covered]);
	useLayoutEffect(() => () => publishHarnessUpdateNoticeRect(null), []);
	const area = panel ?? { left: 0, top: 0, right: document.documentElement.clientWidth, bottom: document.documentElement.clientHeight };
	return <div className="pointer-events-none fixed isolate z-[calc(var(--z-overlay)-1)] overflow-hidden"
		style={{ left: area.left, top: area.top, width: area.right - area.left, height: area.bottom - area.top, borderRadius: panel?.radius }}>
		{/* `harness-update-notice` is the hook styles.css and the clearance tests use for this frame. */}
		<aside ref={frameRef} role="region" aria-label={label} inert={covered} aria-hidden={covered || undefined}
			// Its own compositor layer: the card's height animation repaints only the card.
			// Below the page's top bar when it has one, sliding to match as pages
			// change; `translate` stays clear of each slot's enter animation.
			className={cn("harness-update-notice pointer-events-auto absolute top-3 right-3 w-[min(320px,calc(100%-24px))] text-card-foreground will-change-transform transition-[translate] duration-200 ease-out motion-reduce:transition-none",
				covered && "invisible")}
			// The window's drag region would otherwise swallow the notice's clicks.
			style={{ WebkitAppRegion: "no-drag", translate: `0 ${panel?.topInset ?? 0}px` } as CSSProperties}>
			{children}
		</aside>
	</div>;
}

/** Whether the notice overlaps the live native browser page. */
export function noticeCoversLiveBrowser(browser: ViewportRect | null, notice: ViewportRect | null): boolean {
	if (!browser || !notice) return false;
	return notice.left < browser.right && notice.right > browser.left && notice.top < browser.bottom && notice.bottom > browser.top;
}
