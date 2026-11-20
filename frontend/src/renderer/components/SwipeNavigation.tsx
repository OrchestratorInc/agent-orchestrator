import { ArrowLeft, ArrowRight } from "@/components/app-icons";
import { type RefObject, useEffect, useRef } from "react";
import { aoBridge } from "../lib/bridge";
import { NO_SWIPE_NAV_SELECTOR, resetSwipeClaim, swipeClaimed } from "../lib/swipe-ownership";
import { useUiStore } from "../stores/ui-store";

// Two-finger horizontal trackpad swipes over the center pane go back/forward in
// navigation history, like a browser. A puck follows the fingers in from the pane
// edge and fills once the swipe has gone far enough; lifting the fingers then
// navigates, and pulling back below that point cancels.
//
// DOM wheel events carry no gesture phase. The main process relays Chromium's
// trackpad scroll begin/end, so a held swipe never navigates and momentum after
// the lift is ignored. Without that signal a gesture ends after IDLE_MS without
// wheel events.
//
// A swipe that starts outside the pane, or over content that takes horizontal
// swipes for itself (swipe-to-dismiss cards, tab strips, code blocks, the
// Browser panel, terminals), belongs to that content for the whole scroll.
// Chromium re-targets the rest of a scroll to whatever is under the fingers
// once the element it started on moves away or unmounts, as a swiped-away
// notice does, so the tail of that swipe must not read as a fresh one here.
const IDLE_MS = 120;
// Momentum can also arrive as a new scroll right after the previous one ends.
// Touching the trackpad during momentum looks the same in timing, so a scroll
// that starts this soon after an end is only treated as momentum if it also
// decays from its first event, where a real swipe speeds up first.
const MOMENTUM_GAP_MS = 120;
// Chromium holds the scroll end until momentum finishes, which can take a
// second after a fast swipe. Momentum shows up as steadily shrinking deltas,
// so an armed swipe that was this fast and then decays this many events in a
// row has been released.
const FLICK_SPEED_PX = 10;
const MOMENTUM_DECAY_EVENTS = 4;
const AXIS_DECIDE_PX = 8;
// Accumulated swipe distance that arms navigation.
export const SWIPE_ARM_PX = 110;
const MAX_PX = 220;
// Puck travel: from just past the pane edge to resting inside it.
const PUCK_HIDDEN_PX = -44;
const PUCK_REST_PX = 18;
const PUCK_OVERPULL_PX = 10;

type Direction = "back" | "forward";

export type SwipeNavigationActions = {
	canGoBack: boolean;
	canGoForward: boolean;
	back: () => void;
	forward: () => void;
};

type Gesture = {
	// "owned": the swipe belongs to other content and never navigates.
	mode: "pending" | "tracking" | "done" | "owned";
	sumX: number;
	sumY: number;
	offset: number;
	// Consecutive events with shrinking deltas along the swipe, and the speed
	// the run started from.
	decay: number;
	decayFrom: number;
	lastSpeed: number;
	tracked: number;
	// Started right after a scroll ended and has only decayed since.
	maybeMomentum: boolean;
	// Released into momentum past the arm point: navigates once the event that
	// decided it has reached its target, unless the target took the swipe.
	flick: boolean;
	// Horizontally scrollable ancestors with their starting scrollLeft. If one
	// actually scrolls, the swipe belongs to it, as with browser overscroll.
	scrollers: Array<[HTMLElement, number]>;
};

// Whether a swipe that starts at this path is someone else's: it never reached
// the pane, or it starts over content that opts out.
function ownedElsewhere(path: EventTarget[], boundary: HTMLElement): boolean {
	for (const node of path) {
		if (node === boundary) return false;
		if (node instanceof Element && node.matches(NO_SWIPE_NAV_SELECTOR)) return true;
	}
	return true;
}

// Walks the composed path, not event.target: at the window listener the target of
// an event from inside an open shadow root (the @pierre/diffs code view) is the
// shadow host, which hides the scroller inside it.
function horizontalScrollers(path: EventTarget[], boundary: HTMLElement): Array<[HTMLElement, number]> {
	const scrollers: Array<[HTMLElement, number]> = [];
	for (const node of path) {
		if (node === boundary) break;
		if (node instanceof HTMLElement && node.scrollWidth > node.clientWidth) {
			const overflowX = getComputedStyle(node).overflowX;
			if (overflowX === "auto" || overflowX === "scroll") scrollers.push([node, node.scrollLeft]);
		}
	}
	return scrollers;
}

function prefersReducedMotion(): boolean {
	return typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function SwipeNavigation({
	containerRef,
	navigation,
}: {
	containerRef: RefObject<HTMLElement | null>;
	navigation: SwipeNavigationActions;
}) {
	const enabled = useUiStore((state) => state.navigationGestures);
	const backRef = useRef<HTMLDivElement>(null);
	const forwardRef = useRef<HTMLDivElement>(null);
	const historyState = useRef(navigation);
	historyState.current = navigation;

	useEffect(() => {
		const container = containerRef.current;
		if (!container || !enabled) return;
		let gesture: Gesture | null = null;
		let idleTimer: number | undefined;
		let frame = 0;
		let nativePhases = false;
		let fingersDown = false;
		let lastEndAt = Number.NEGATIVE_INFINITY;
		let lastOwnedEndAt = Number.NEGATIVE_INFINITY;
		let snapPending = false;

		const puck = (direction: Direction) => (direction === "back" ? backRef.current : forwardRef.current);

		// Writes the puck position straight to the DOM; wheel events arrive far
		// faster than React should re-render. A gesture's first paint snaps the
		// pucks back to their idle look, so a previous gesture's armed fill never
		// fades out on the next one.
		const paint = (offset: number) => {
			cancelAnimationFrame(frame);
			frame = requestAnimationFrame(() => {
				const fresh = snapPending;
				snapPending = false;
				for (const direction of ["back", "forward"] as const) {
					const el = puck(direction);
					if (!el) continue;
					const distance = direction === "back" ? Math.max(0, offset) : Math.max(0, -offset);
					const progress = Math.min(1, distance / SWIPE_ARM_PX);
					const overpull = Math.max(0, distance - SWIPE_ARM_PX);
					const eased = 1 - (1 - progress) ** 3;
					const travel =
						PUCK_HIDDEN_PX +
						(PUCK_REST_PX - PUCK_HIDDEN_PX) * eased +
						PUCK_OVERPULL_PX * (1 - Math.exp(-overpull / 60));
					const sign = direction === "back" ? 1 : -1;
					el.style.transition = fresh ? "none" : "background-color 120ms, color 120ms, border-color 120ms";
					el.style.opacity = String(Math.min(1, progress * 2.5));
					el.style.transform = prefersReducedMotion()
						? `translate3d(${sign * PUCK_REST_PX}px, -50%, 0)`
						: `translate3d(${sign * travel}px, -50%, 0) scale(${0.72 + 0.28 * progress})`;
					el.toggleAttribute("data-armed", distance >= SWIPE_ARM_PX);
				}
			});
		};

		// Fades the pucks out where they are.
		const release = () => {
			cancelAnimationFrame(frame);
			for (const direction of ["back", "forward"] as const) {
				const el = puck(direction);
				if (!el) continue;
				el.style.transition = "opacity 150ms ease-in";
				el.style.opacity = "0";
			}
		};

		const canNavigate = (direction: Direction) =>
			direction === "back"
				? historyState.current.canGoBack
				: historyState.current.canGoForward;

		const navigate = (offset: number) => {
			const direction: Direction = offset > 0 ? "back" : "forward";
			if (Math.abs(offset) < SWIPE_ARM_PX || !canNavigate(direction)) return;
			if (direction === "back") historyState.current.back();
			else historyState.current.forward();
		};

		const endGesture = () => {
			window.clearTimeout(idleTimer);
			idleTimer = undefined;
			const ended = gesture;
			gesture = null;
			if (!ended) return;
			const claimed = swipeClaimed();
			// Content that scrolled, even on the swipe's only event, took it.
			const scrolled = ended.scrollers.some(([el, start]) => el.scrollLeft !== start);
			if (ended.mode === "owned" || claimed || scrolled) lastOwnedEndAt = performance.now();
			// A flick whose deciding event never bubbled back to the window.
			if (ended.flick) {
				if (!claimed) navigate(ended.offset);
				return;
			}
			if (ended.mode !== "tracking") return;
			release();
			if (claimed || scrolled) return;
			navigate(ended.offset);
		};

		// Runs after the event's own handlers, which the capture listener cannot
		// see: one that claimed the swipe took it, even on its last event. A
		// prevented default says nothing here; modal scroll locks prevent every
		// wheel event that cannot scroll.
		const afterWheel = () => {
			if (!gesture) return;
			const taken = swipeClaimed();
			// Including a flick decided on this event: its momentum tail stays owned.
			if (taken && gesture.mode !== "owned") {
				gesture.mode = "owned";
				release();
			}
			if (!gesture.flick) return;
			gesture.flick = false;
			if (!taken) navigate(gesture.offset);
		};

		const onWheel = (event: WheelEvent) => {
			// Pinch-zoom arrives as ctrl+wheel; line/page deltas come from mouse wheels.
			if (event.ctrlKey || event.deltaMode !== WheelEvent.DOM_DELTA_PIXEL) return;
			// With native phases, wheel events after the lift are momentum (or a mouse).
			if (nativePhases && !fingersDown) return;
			if (!nativePhases) {
				window.clearTimeout(idleTimer);
				idleTimer = window.setTimeout(endGesture, IDLE_MS);
			}

			if (!gesture) {
				snapPending = true;
				resetSwipeClaim();
				const path = event.composedPath();
				const now = performance.now();
				// Momentum can arrive as a new scroll right after the lift; after a
				// swipe that belonged elsewhere, it belongs there too.
				const owned = ownedElsewhere(path, container) || (nativePhases && now - lastOwnedEndAt <= MOMENTUM_GAP_MS);
				gesture = {
					mode: owned ? "owned" : "pending",
					sumX: 0,
					sumY: 0,
					offset: 0,
					decay: 0,
					decayFrom: 0,
					lastSpeed: 0,
					tracked: 0,
					maybeMomentum: now - lastEndAt <= MOMENTUM_GAP_MS,
					flick: false,
					scrollers: owned ? [] : horizontalScrollers(path, container),
				};
			}
			if (gesture.mode === "done" || gesture.mode === "owned") return;
			// Content that scrolled with the swipe, or a handler that claimed it, keeps it.
			if (swipeClaimed() || gesture.scrollers.some(([el, start]) => el.scrollLeft !== start)) {
				gesture.mode = "owned";
				release();
				return;
			}

			let deltaX = event.deltaX;
			if (gesture.mode === "pending") {
				gesture.sumX += event.deltaX;
				gesture.sumY += event.deltaY;
				if (Math.abs(gesture.sumX) + Math.abs(gesture.sumY) < AXIS_DECIDE_PX) return;
				if (Math.abs(gesture.sumX) <= Math.abs(gesture.sumY) * 1.5) {
					gesture.mode = "done";
					return;
				}
				gesture.mode = "tracking";
				deltaX = gesture.sumX;
			}

			// Fingers moving right scroll toward the left edge (negative deltaX):
			// that reveals the back puck from the left, as in Safari and Chrome.
			let offset = Math.max(-MAX_PX, Math.min(MAX_PX, gesture.offset - deltaX));
			if (offset > 0 && !canNavigate("back")) offset = 0;
			if (offset < 0 && !canNavigate("forward")) offset = 0;
			gesture.offset = offset;
			paint(offset);

			const speed = Math.abs(deltaX);
			const alongSwipe = offset !== 0 && Math.sign(-deltaX) === Math.sign(offset);
			if (alongSwipe && speed < gesture.lastSpeed) gesture.decay += 1;
			else {
				if (gesture.tracked > 0) gesture.maybeMomentum = false;
				gesture.decay = 0;
				gesture.decayFrom = speed;
			}
			gesture.lastSpeed = speed;
			gesture.tracked += 1;
			if (gesture.decay >= MOMENTUM_DECAY_EVENTS && gesture.maybeMomentum) {
				// Momentum from the previous swipe, not a new one.
				gesture.mode = "done";
				release();
				return;
			}
			if (gesture.decay >= MOMENTUM_DECAY_EVENTS && gesture.decayFrom >= FLICK_SPEED_PX && Math.abs(offset) >= SWIPE_ARM_PX) {
				// Released into momentum: navigate as soon as this event has been
				// handled (afterWheel) and ignore the rest of the scroll.
				gesture.mode = "done";
				gesture.flick = true;
				release();
			}
		};

		const stopPhases = aoBridge.app.onScrollPhase((phase) => {
			nativePhases = true;
			if (phase === "down") {
				fingersDown = true;
				window.clearTimeout(idleTimer);
				return;
			}
			lastEndAt = performance.now();
			fingersDown = false;
			endGesture();
		});
		// The whole window, not just the pane: a swipe is followed from wherever it
		// starts, so the rest of one that began elsewhere is known to be its tail.
		window.addEventListener("wheel", onWheel, { capture: true, passive: true });
		window.addEventListener("wheel", afterWheel, { passive: true });
		return () => {
			stopPhases();
			window.removeEventListener("wheel", onWheel, { capture: true });
			window.removeEventListener("wheel", afterWheel);
			window.clearTimeout(idleTimer);
			cancelAnimationFrame(frame);
		};
	}, [containerRef, enabled]);

	if (!enabled) return null;

	// The pucks rest just past the pane's edges. Clip them in a box that cannot
	// scroll: a container with overflow hidden can still be scrolled by code
	// (scrollIntoView, focus), and 44px of overhang would then slide the whole
	// pane sideways under the sidebar.
	return (
		<div aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-clip" data-swipe-pucks="">
			<SwipePuck direction="back" puckRef={backRef} />
			<SwipePuck direction="forward" puckRef={forwardRef} />
		</div>
	);
}

function SwipePuck({ direction, puckRef }: { direction: Direction; puckRef: RefObject<HTMLDivElement | null> }) {
	const Icon = direction === "back" ? ArrowLeft : ArrowRight;
	return (
		<div
			aria-hidden="true"
			className={`pointer-events-none absolute top-1/2 z-chrome grid size-control-xl place-items-center rounded-full border border-border-strong bg-popover text-muted-foreground opacity-0 shadow-popover data-armed:border-foreground data-armed:bg-foreground data-armed:text-on-foreground ${direction === "back" ? "left-0" : "right-0"}`}
			data-swipe-puck={direction}
			ref={puckRef}
			style={{ transform: `translate3d(${direction === "back" ? PUCK_HIDDEN_PX : -PUCK_HIDDEN_PX}px, -50%, 0)` }}
		>
			<Icon className="size-icon-xl" />
		</div>
	);
}
