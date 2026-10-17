import { ArrowLeft, ArrowRight } from "lucide-react";
import { type RefObject, useEffect, useRef } from "react";
import { aoBridge } from "../lib/bridge";

// Two-finger horizontal trackpad swipes over the center pane go back/forward in
// navigation history, like a browser. A puck follows the fingers in from the pane
// edge and fills once the swipe has gone far enough; lifting the fingers then
// navigates, and pulling back below that point cancels.
//
// DOM wheel events carry no gesture phase. On macOS the main process relays
// trackpad finger down/up (native/scroll-phase), so a held swipe never
// navigates and momentum after the lift is ignored. Without that signal
// (Windows/Linux, or the addon failed to load) a gesture ends after IDLE_MS
// without wheel events.
const IDLE_MS = 120;
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
	mode: "pending" | "tracking" | "done";
	sumX: number;
	sumY: number;
	offset: number;
	// Horizontally scrollable ancestors with their starting scrollLeft. If one
	// actually scrolls, the swipe belongs to it, as with browser overscroll.
	scrollers: Array<[HTMLElement, number]>;
};

function horizontalScrollers(target: EventTarget | null, boundary: HTMLElement): Array<[HTMLElement, number]> {
	const scrollers: Array<[HTMLElement, number]> = [];
	let el = target instanceof Element ? target : null;
	while (el && el !== boundary) {
		if (el instanceof HTMLElement && el.scrollWidth > el.clientWidth) {
			const overflowX = getComputedStyle(el).overflowX;
			if (overflowX === "auto" || overflowX === "scroll") scrollers.push([el, el.scrollLeft]);
		}
		el = el.parentElement;
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
	const backRef = useRef<HTMLDivElement>(null);
	const forwardRef = useRef<HTMLDivElement>(null);
	const historyState = useRef(navigation);
	historyState.current = navigation;

	useEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		let gesture: Gesture | null = null;
		let idleTimer: number | undefined;
		let frame = 0;
		let nativePhases = false;
		let fingersDown = false;
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

		const endGesture = () => {
			window.clearTimeout(idleTimer);
			idleTimer = undefined;
			const ended = gesture;
			gesture = null;
			if (!ended || ended.mode !== "tracking") return;
			release();
			if (ended.scrollers.some(([el, start]) => el.scrollLeft !== start)) return;
			const direction: Direction = ended.offset > 0 ? "back" : "forward";
			if (Math.abs(ended.offset) < SWIPE_ARM_PX || !canNavigate(direction)) return;
			if (direction === "back") historyState.current.back();
			else historyState.current.forward();
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

			if (!gesture) snapPending = true;
			gesture ??= { mode: "pending", sumX: 0, sumY: 0, offset: 0, scrollers: horizontalScrollers(event.target, container) };
			if (gesture.mode === "done") return;
			if (gesture.scrollers.some(([el, start]) => el.scrollLeft !== start)) {
				gesture.mode = "done";
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
		};

		const stopPhases = aoBridge.app.onScrollPhase((phase) => {
			nativePhases = true;
			if (phase === "down") {
				fingersDown = true;
				window.clearTimeout(idleTimer);
				return;
			}
			fingersDown = false;
			if (phase === "cancel") {
				window.clearTimeout(idleTimer);
				gesture = null;
				release();
				return;
			}
			endGesture();
		});
		container.addEventListener("wheel", onWheel, { capture: true, passive: true });
		return () => {
			stopPhases();
			container.removeEventListener("wheel", onWheel, { capture: true });
			window.clearTimeout(idleTimer);
			cancelAnimationFrame(frame);
		};
	}, [containerRef]);

	return (
		<>
			<SwipePuck direction="back" puckRef={backRef} />
			<SwipePuck direction="forward" puckRef={forwardRef} />
		</>
	);
}

function SwipePuck({ direction, puckRef }: { direction: Direction; puckRef: RefObject<HTMLDivElement | null> }) {
	const Icon = direction === "back" ? ArrowLeft : ArrowRight;
	return (
		<div
			aria-hidden="true"
			className={`pointer-events-none absolute top-1/2 z-chrome grid size-control-xl place-items-center rounded-full border border-border-strong bg-popover text-muted-foreground opacity-0 shadow-popover data-armed:border-foreground data-armed:bg-foreground data-armed:text-background ${direction === "back" ? "left-0" : "right-0"}`}
			data-swipe-puck={direction}
			ref={puckRef}
			style={{ transform: `translate3d(${direction === "back" ? PUCK_HIDDEN_PX : -PUCK_HIDDEN_PX}px, -50%, 0)` }}
		>
			<Icon className="size-icon-xl" />
		</div>
	);
}
