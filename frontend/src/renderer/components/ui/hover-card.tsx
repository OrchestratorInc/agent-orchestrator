import { HoverCard as HoverCardPrimitive } from "radix-ui";
import {
	createContext,
	useCallback,
	useContext,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type ComponentProps,
	type Ref,
	type RefObject,
} from "react";
import { useLatestCallback } from "../../hooks/useStable";
import { cn } from "../../lib/utils";

/**
 * A card closes the moment what it describes is acted on or goes away: its
 * trigger is clicked, a link on it is followed, the route changes, the window
 * loses focus, the page or a container holding the trigger scrolls, or the
 * trigger stops being laid out (a chat kept alive in a hidden <Activity>,
 * display: none, or removed). Without a laid-out trigger, Floating UI would
 * place the card at the window's top-left corner.
 */
type HoverCardDismiss = {
	/** Closes the card if it is open. */
	dismiss: () => void;
	/** Closes the card unconditionally, for the card's own content: mounted means open or closing. */
	close: () => void;
	allowReopen: () => void;
	trigger: RefObject<HTMLElement | null>;
};

const HoverCardDismissContext = createContext<HoverCardDismiss | null>(null);

// Every open card, for closing them all when the route changes.
const openCards = new Set<() => void>();

/** Closes every open hover card. The router calls this when the path changes. */
export function dismissHoverCards() {
	for (const dismiss of Array.from(openCards)) dismiss();
}

/** Closes the hover card this is rendered in, for a card whose own click opens what it describes. */
export function useHoverCardDismiss(): () => void {
	return useContext(HoverCardDismissContext)?.close ?? noop;
}

function noop() {}

/**
 * A callback ref that hands the node to `own` and to the forwarded `ref`. It
 * returns a cleanup, so React detaches through it: a forwarded callback ref's
 * own React 19 cleanup runs then, or it is called with null as before.
 */
function useMergedRef<T>(ref: Ref<T> | undefined, own: (node: T | null) => void) {
	return useCallback(
		(node: T | null) => {
			own(node);
			const cleanup = typeof ref === "function" ? ref(node) : undefined;
			if (ref && typeof ref !== "function") ref.current = node;
			return () => {
				own(null);
				if (typeof cleanup === "function") cleanup();
				else if (typeof ref === "function") ref(null);
				else if (ref) ref.current = null;
			};
		},
		[own, ref],
	);
}

/** Connected and laid out: not inside display: none, which a hidden <Activity> uses. */
function isRendered(element: HTMLElement | null): boolean {
	if (!element?.isConnected) return false;
	// jsdom has no layout and no checkVisibility, so connected is all it can tell.
	return typeof element.checkVisibility !== "function" || element.checkVisibility();
}

function HoverCard({
	openDelay = 300,
	closeDelay = 150,
	open: openProp,
	defaultOpen = false,
	onOpenChange,
	dismissOnScroll = true,
	...props
}: ComponentProps<typeof HoverCardPrimitive.Root> & {
	/** False for an owner that follows scrolling itself, such as the sidebar's preview. */
	dismissOnScroll?: boolean;
}) {
	const controlled = openProp !== undefined;
	const [uncontrolledOpen, setUncontrolledOpen] = useState(defaultOpen);
	const open = controlled ? openProp : uncontrolledOpen;
	const openRef = useRef(open);
	useLayoutEffect(() => {
		openRef.current = open;
	});
	// Set by a dismissal and cleared when the pointer next enters or leaves the
	// trigger or focus leaves it. Until then Radix may not reopen the card: the
	// focus a click gives the trigger starts an open timer that would fire after
	// the click closed it.
	const suppressed = useRef(false);
	const triggerRef = useRef<HTMLElement | null>(null);
	const reportOpenChange = useLatestCallback((next: boolean) => onOpenChange?.(next));
	const setOpen = useCallback(
		(next: boolean) => {
			if (next && suppressed.current) return;
			if (!controlled) setUncontrolledOpen(next);
			reportOpenChange(next);
		},
		[controlled, reportOpenChange],
	);
	const close = useCallback(() => {
		suppressed.current = true;
		setOpen(false);
	}, [setOpen]);
	// The trigger is clicked whether or not its card is showing. openRef is
	// synced after commit, which is fine here but too late for the content's own
	// layout effect on the commit that opens it, so that uses close.
	const dismiss = useCallback(() => {
		if (openRef.current) close();
		else suppressed.current = true;
	}, [close]);
	const allowReopen = useCallback(() => {
		suppressed.current = false;
	}, []);
	useLayoutEffect(() => {
		if (!open) return;
		openCards.add(dismiss);
		return () => {
			openCards.delete(dismiss);
		};
	}, [dismiss, open]);
	// Radix closes a card only on pointer leave, and a scroll moves the trigger
	// out from under a pointer that stays put: in a chat that auto-scrolls while
	// it streams, the card never got a leave event and stayed open over the
	// timeline. Only a scroll that can move the trigger closes it: the page, or a
	// container the trigger sits in. Scrolling inside the card or another pane
	// keeps it open.
	useEffect(() => {
		if (!open || !dismissOnScroll) return;
		const onScroll = (event: Event) => {
			const target = event.target;
			if (target instanceof Element && target.closest("[data-slot=hover-card-content]")) return;
			const trigger = triggerRef.current;
			const movesTrigger =
				target === window ||
				target === document ||
				(target instanceof Node && trigger !== null && target.contains(trigger));
			if (movesTrigger) close();
		};
		window.addEventListener("scroll", onScroll, { capture: true, passive: true });
		return () => window.removeEventListener("scroll", onScroll, { capture: true });
	}, [close, dismissOnScroll, open]);
	const context = useMemo(() => ({ dismiss, close, allowReopen, trigger: triggerRef }), [allowReopen, close, dismiss]);
	return (
		<HoverCardDismissContext.Provider value={context}>
			<HoverCardPrimitive.Root
				data-slot="hover-card"
				openDelay={openDelay}
				closeDelay={closeDelay}
				open={open}
				onOpenChange={setOpen}
				{...props}
			/>
		</HoverCardDismissContext.Provider>
	);
}

function HoverCardTrigger({
	ref,
	onClick,
	onPointerEnter,
	onPointerLeave,
	onBlur,
	...props
}: ComponentProps<typeof HoverCardPrimitive.Trigger>) {
	const card = useContext(HoverCardDismissContext);
	const fallbackRef = useRef<HTMLElement | null>(null);
	const triggerRef = card?.trigger ?? fallbackRef;
	const setTrigger = useCallback((node: HTMLElement | null) => {
		triggerRef.current = node;
	}, [triggerRef]);
	const mergedRef = useMergedRef(ref as Ref<HTMLElement> | undefined, setTrigger);
	return (
		<HoverCardPrimitive.Trigger
			data-slot="hover-card-trigger"
			ref={mergedRef}
			// Activating the link or row opens what the card describes, which is
			// often another session: the card has done its job.
			onClick={(event) => {
				onClick?.(event);
				card?.dismiss();
			}}
			onPointerEnter={(event) => {
				card?.allowReopen();
				onPointerEnter?.(event);
			}}
			onPointerLeave={(event) => {
				card?.allowReopen();
				onPointerLeave?.(event);
			}}
			onBlur={(event) => {
				card?.allowReopen();
				onBlur?.(event);
			}}
			{...props}
		/>
	);
}

/**
 * Closes the card when its trigger stops being laid out, hiding it at once so
 * it is never drawn at the top-left fallback position, and when the window
 * loses focus.
 */
function useDismissWhenTriggerHidden(card: HoverCardDismiss | null, content: HTMLElement | null) {
	useLayoutEffect(() => {
		if (!card || !content) return;
		const hideIfTriggerHidden = () => {
			if (!content.isConnected || isRendered(card.trigger.current)) return false;
			content.style.display = "none";
			card.close();
			return true;
		};
		if (hideIfTriggerHidden()) return;
		const trigger = card.trigger.current;
		// Resize covers a trigger with a size losing its box; intersection covers a
		// zero-size anchor such as the terminal's, which has no size to lose.
		const resize = trigger && typeof ResizeObserver !== "undefined" ? new ResizeObserver(hideIfTriggerHidden) : undefined;
		const intersection = trigger && typeof IntersectionObserver !== "undefined" ? new IntersectionObserver(hideIfTriggerHidden) : undefined;
		if (trigger) {
			resize?.observe(trigger);
			intersection?.observe(trigger);
		}
		// Focus moving into a frame on the page (an HTML artifact preview's page
		// focusing itself) also blurs the window, but leaves the document focused.
		let blurCheck: ReturnType<typeof setTimeout> | undefined;
		const onWindowBlur = () => {
			clearTimeout(blurCheck);
			blurCheck = setTimeout(() => {
				if (!document.hasFocus()) card.close();
			}, 0);
		};
		window.addEventListener("blur", onWindowBlur);
		return () => {
			resize?.disconnect();
			intersection?.disconnect();
			clearTimeout(blurCheck);
			window.removeEventListener("blur", onWindowBlur);
			// A chat kept alive in a hidden <Activity> runs this cleanup, then sets
			// display: none on its own DOM but not on this portal, and runs no
			// effects until shown again. Check once that commit is done. A plain
			// unmount has disconnected the card by then, and StrictMode's rerun finds
			// the trigger still laid out.
			queueMicrotask(hideIfTriggerHidden);
		};
	}, [card, content]);
}

function HoverCardContent({
	className,
	align = "start",
	sideOffset = 6,
	portalContainer,
	animation = "full",
	ref,
	onClickCapture,
	...props
}: ComponentProps<typeof HoverCardPrimitive.Content> & {
	portalContainer?: ComponentProps<typeof HoverCardPrimitive.Portal>["container"];
	/**
	 * "full" fades and zooms in and out. "enter" fades and slides in only, so
	 * closing is instant. "none" appears and disappears instantly.
	 */
	animation?: "full" | "enter" | "none";
}) {
	const card = useContext(HoverCardDismissContext);
	// State, not a ref: Radix's Portal mounts the card a commit after this
	// component (and its Presence unmounts and remounts it while this stays
	// rendered), so the watcher must run whenever the element itself arrives.
	const [content, setContent] = useState<HTMLDivElement | null>(null);
	const mergedRef = useMergedRef(ref, setContent);
	useDismissWhenTriggerHidden(card, content);
	return (
		<HoverCardPrimitive.Portal container={portalContainer}>
			<HoverCardPrimitive.Content
				data-slot="hover-card-content"
				ref={mergedRef}
				align={align}
				sideOffset={sideOffset}
				// Capture, so a link that stops its click still closes the card it leaves.
				onClickCapture={(event) => {
					onClickCapture?.(event);
					if (event.target instanceof Element && event.target.closest("a[href]")) card?.close();
				}}
				className={cn(
					"z-overlay w-72 rounded-lg border border-(--color-border-overlay) bg-overlay text-popover-foreground shadow-(--shadow-popover) outline-none",
					animation === "full" &&
						"origin-(--radix-hover-card-content-transform-origin) animate-in fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
					animation === "enter" &&
						"animate-in fade-in-0 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
					className,
				)}
				{...props}
			/>
		</HoverCardPrimitive.Portal>
	);
}

export { HoverCard, HoverCardTrigger, HoverCardContent };
