import { type RefObject, useCallback, useEffect, useRef } from "react";
import { aoBridge } from "../lib/bridge";

// A two-finger horizontal trackpad swipe, or a pointer drag, to the right moves
// the element with the fingers. Letting go past DISMISS_FRACTION of its width,
// or flicking it, slides it out and dismisses it; anything shorter springs back.
// As in SwipeNavigation, the main process relays native scroll begin/end, so
// momentum after the lift is ignored; without that signal a gesture ends after
// IDLE_MS without wheel events.
const IDLE_MS = 120;
const AXIS_DECIDE_PX = 8;
const DISMISS_FRACTION = 0.35;
// Pixels per wheel event (or per 16ms of pointer travel) that count as a flick.
const FLICK_SPEED_PX = 12;
// Chromium holds the scroll end until momentum finishes, which can take a
// second after a fast swipe. Momentum shows up as steadily shrinking deltas, so
// a fast swipe that then decays this many events in a row has been released.
const MOMENTUM_DECAY_EVENTS = 4;
const OUT_MS = 160;
const FADE_MS = 120;
const CLOSE_MS = 180;
const BACK_MS = 200;

// Set once a swipe is released into momentum and dismissed early. Shared by
// every swipeable element so the rest of that scroll does not swipe away the
// next one that slides under the pointer.
let scrollSpent = false;
let scrollSpentTimer: number | undefined;
let mountedCount = 0;

function prefersReducedMotion(): boolean {
	return typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/** Slides the element out to the right (a swipe) or fades it in place (the ×
 * button), then closes the gap it leaves so the content below moves up instead
 * of jumping, then calls `done`. */
function leave(element: HTMLElement, from: number, done: () => void, style: "slide" | "fade" = "slide"): number {
	if (prefersReducedMotion()) {
		done();
		return 0;
	}
	const outMs = style === "slide" ? OUT_MS : FADE_MS;
	element.style.transition = style === "slide" ? `transform ${outMs}ms var(--ease-out), opacity ${outMs}ms var(--ease-out)` : `opacity ${outMs}ms var(--ease-out)`;
	if (style === "slide") element.style.transform = `translate3d(${Math.max(from, 0) + (element.offsetWidth || 1) + 24}px, 0, 0)`;
	element.style.opacity = "0";
	return window.setTimeout(() => {
		element.style.height = `${element.offsetHeight}px`;
		element.style.overflow = "hidden";
		// Commit the fixed height before animating it away.
		void element.offsetHeight;
		element.style.transition = `height ${CLOSE_MS}ms var(--ease-out), padding ${CLOSE_MS}ms var(--ease-out), margin ${CLOSE_MS}ms var(--ease-out), border-width ${CLOSE_MS}ms var(--ease-out)`;
		element.style.height = "0px";
		element.style.paddingTop = "0px";
		element.style.paddingBottom = "0px";
		element.style.marginTop = "0px";
		element.style.borderWidth = "0px";
		window.setTimeout(done, CLOSE_MS);
	}, outMs);
}

/** Undoes `leave`, for a dismissal that failed. */
function restore(element: HTMLElement): void {
	for (const property of ["transition", "transform", "opacity", "height", "overflow", "padding-top", "padding-bottom", "margin-top", "border-width"]) {
		element.style.removeProperty(property);
	}
}

/** Wires swipe-to-dismiss on `ref` and returns a quick fade-out for a × button.
 * If `onDismiss` resolves to false the dismissal failed and the element comes back. */
export function useSwipeToDismiss(ref: RefObject<HTMLElement | null>, onDismiss: () => void | Promise<boolean>, enabled = true): () => void {
	const dismissRef = useRef(onDismiss);
	dismissRef.current = onDismiss;
	const leaving = useRef(false);
	const complete = useCallback(() => {
		void Promise.resolve(dismissRef.current()).then((dismissed) => {
			const element = ref.current;
			if (dismissed !== false || !element) return;
			restore(element);
			leaving.current = false;
		});
	}, [ref]);
	const dismiss = useCallback(() => {
		const element = ref.current;
		if (leaving.current) return;
		leaving.current = true;
		if (!element) complete();
		else leave(element, 0, complete, "fade");
	}, [complete, ref]);

	useEffect(() => {
		const element = ref.current;
		if (!element || !enabled) return;
		let mode: "idle" | "pending" | "tracking" | "ignored" = "idle";
		let sumX = 0;
		let sumY = 0;
		let offset = 0;
		let speed = 0;
		let lastSpeed = 0;
		let decayFrom = 0;
		let decay = 0;
		let idleTimer: number | undefined;
		let outTimer: number | undefined;
		let nativePhases = false;
		let fingersDown = false;

		// Written straight to the DOM: wheel events arrive far faster than React should re-render.
		const paint = (x: number, ms = 0) => {
			element.style.transition = ms ? `transform ${ms}ms var(--ease-out), opacity ${ms}ms var(--ease-out)` : "none";
			element.style.transform = x > 0 && !prefersReducedMotion() ? `translate3d(${x}px, 0, 0)` : "";
			element.style.opacity = x > 0 ? String(Math.max(0, 1 - x / (element.offsetWidth || 1))) : "";
		};

		const track = (x: number) => {
			offset = Math.max(0, x);
			paint(offset);
		};

		const finish = () => {
			window.clearTimeout(idleTimer);
			idleTimer = undefined;
			const tracked = mode === "tracking";
			mode = "idle";
			sumX = 0;
			sumY = 0;
			lastSpeed = 0;
			decayFrom = 0;
			decay = 0;
			if (!tracked) return;
			const width = element.offsetWidth || 1;
			if (offset >= width * DISMISS_FRACTION || (offset > AXIS_DECIDE_PX && speed >= FLICK_SPEED_PX)) {
				leaving.current = true;
				outTimer = leave(element, offset, complete);
				return;
			}
			offset = 0;
			paint(0, BACK_MS);
		};

		// Settles the axis once the gesture has moved far enough; only a mostly
		// horizontal one is a swipe, so vertical scrolling inside still works.
		const decide = (dx: number, dy: number): boolean => {
			if (Math.abs(dx) + Math.abs(dy) < AXIS_DECIDE_PX) return false;
			mode = Math.abs(dx) > Math.abs(dy) * 1.5 ? "tracking" : "ignored";
			return mode === "tracking";
		};

		const onWheel = (event: WheelEvent) => {
			// Pinch-zoom arrives as ctrl+wheel; line/page deltas come from mouse wheels.
			if (leaving.current || event.ctrlKey || event.deltaMode !== WheelEvent.DOM_DELTA_PIXEL) return;
			if (nativePhases && !fingersDown) return;
			if (scrollSpent) {
				if (!nativePhases) {
					window.clearTimeout(scrollSpentTimer);
					scrollSpentTimer = window.setTimeout(() => { scrollSpent = false; }, IDLE_MS);
				}
				return;
			}
			if (!nativePhases) {
				window.clearTimeout(idleTimer);
				idleTimer = window.setTimeout(finish, IDLE_MS);
			}
			if (mode === "ignored") return;
			if (mode === "idle") mode = "pending";
			// Fingers moving right scroll toward the left edge (negative deltaX).
			speed = -event.deltaX;
			if (mode === "pending") {
				sumX += event.deltaX;
				sumY += event.deltaY;
				if (decide(sumX, sumY)) track(-sumX);
				return;
			}
			track(offset - event.deltaX);
			if (speed > 0 && speed < lastSpeed) decay += 1;
			else {
				decay = 0;
				decayFrom = speed;
			}
			lastSpeed = speed;
			// Only past the dismiss point, where lifting would dismiss anyway: fingers
			// that are still down and slowing look the same as momentum.
			if (decay >= MOMENTUM_DECAY_EVENTS && decayFrom >= FLICK_SPEED_PX && offset >= (element.offsetWidth || 1) * DISMISS_FRACTION) {
				// Released into momentum: dismiss now instead of after it.
				speed = decayFrom;
				scrollSpent = true;
				if (!nativePhases) scrollSpentTimer = window.setTimeout(() => { scrollSpent = false; }, IDLE_MS);
				finish();
			}
		};

		const onPointerDown = (event: PointerEvent) => {
			// A stale wheel gesture must not block a drag; only a swipe in progress does.
			if (leaving.current || event.button !== 0 || mode === "tracking") return;
			if (event.target instanceof Element && event.target.closest("button, a, input, textarea, select")) return;
			const startX = event.clientX;
			const startY = event.clientY;
			let lastX = startX;
			let lastTime = event.timeStamp;
			mode = "pending";
			const onMove = (move: PointerEvent) => {
				if (move.pointerId !== event.pointerId) return;
				const dx = move.clientX - startX;
				if (mode === "pending" && decide(dx, move.clientY - startY)) element.setPointerCapture?.(event.pointerId);
				if (mode !== "tracking") return;
				speed = ((move.clientX - lastX) / Math.max(1, move.timeStamp - lastTime)) * 16;
				lastX = move.clientX;
				lastTime = move.timeStamp;
				track(dx);
			};
			const onUp = (up: PointerEvent) => {
				if (up.pointerId !== event.pointerId) return;
				window.removeEventListener("pointermove", onMove);
				window.removeEventListener("pointerup", onUp);
				window.removeEventListener("pointercancel", onUp);
				// The click that ends a drag must not also activate the element.
				if (mode === "tracking") {
					const swallow = (click: MouseEvent) => click.stopPropagation();
					element.addEventListener("click", swallow, { capture: true, once: true });
					window.setTimeout(() => element.removeEventListener("click", swallow, { capture: true }));
				}
				finish();
			};
			window.addEventListener("pointermove", onMove);
			window.addEventListener("pointerup", onUp);
			window.addEventListener("pointercancel", onUp);
		};

		const stopPhases = aoBridge.app.onScrollPhase((phase) => {
			nativePhases = true;
			scrollSpent = false;
			if (phase === "down") {
				fingersDown = true;
				window.clearTimeout(idleTimer);
				return;
			}
			fingersDown = false;
			if (mode !== "idle") finish();
		});
		element.addEventListener("wheel", onWheel, { passive: true });
		element.addEventListener("pointerdown", onPointerDown);
		// A native drag would cancel the pointer stream mid-gesture.
		const onDragStart = (event: DragEvent) => event.preventDefault();
		element.addEventListener("dragstart", onDragStart);
		mountedCount += 1;
		return () => {
			// Nothing left to swipe, so no momentum left to swallow.
			mountedCount -= 1;
			if (mountedCount === 0) scrollSpent = false;
			element.removeEventListener("dragstart", onDragStart);
			stopPhases();
			element.removeEventListener("wheel", onWheel);
			element.removeEventListener("pointerdown", onPointerDown);
			window.clearTimeout(idleTimer);
			window.clearTimeout(outTimer);
		};
	}, [complete, enabled, ref]);
	return dismiss;
}
