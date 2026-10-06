import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef } from "react";

/** Writes this close to the current position are skipped. */
const SETTLE_PX = 0.5;
/** A scroll event within this distance of our last written position is ours. */
const OWN_SCROLL_PX = 2;
/**
 * Streamed growth up to this size is followed with an instant write. Streamed text
 * already grows smoothly, so keeping the end in place reads as smooth; restarting a
 * native smooth scroll every chunk would replay its ease-in and trail behind.
 */
const FOLLOW_INSTANT_PX = 48;
/** A smooth scroll in flight is re-aimed only when its target moves this much. */
const REAIM_PX = 48;
/** A native smooth scroll is long over by now even if its end event was missed. */
const FLIGHT_MAX_MS = 1500;

/**
 * Tests snap, as under reduced motion, because they assert scroll positions
 * synchronously and jsdom has no layout to animate. The test-mode branch is
 * compiled out of production builds.
 */
const CANNOT_ANIMATE = import.meta.env.MODE === "test";

type Flight = { from: number; to: number; aimedAt: number };

/**
 * Moves a scroll viewport toward its end without snapping and without stutter.
 *
 * Large moves (a send, Jump to latest, a big append) use the browser's native
 * smooth scroll, which Chromium animates on the compositor, so it keeps moving
 * while the main thread is busy committing. Small streamed growth is followed with
 * instant writes. Every position along a smooth scroll in flight counts as ours;
 * reader input stops it in place (`cancel` / `hold`). Reduced motion snaps.
 */
export function useScrollFollow(getNode: () => HTMLElement | null) {
	const reducedMotion = useReducedMotion();
	const snap = useRef(false);
	snap.current = CANNOT_ANIMATE || Boolean(reducedMotion);
	// Where our last write left (or will leave) the viewport. Scroll events are
	// classified by position rather than by time: an event that lands here is ours
	// however late it arrives, and anything else is the reader's.
	const lastWritten = useRef<number | null>(null);
	const flight = useRef<Flight | null>(null);
	// When we last aimed a smooth scroll; it may still be animating after `flight`
	// is cleared, so cancel keys off this too.
	const lastAimAt = useRef(0);
	const holdUntil = useRef(0);

	const endOf = (node: HTMLElement) => Math.max(0, node.scrollHeight - node.clientHeight);
	const write = (node: HTMLElement, top: number) => {
		if (Math.abs(node.scrollTop - top) <= SETTLE_PX) return;
		node.scrollTop = top;
		lastWritten.current = node.scrollTop;
	};
	const smoothTo = (node: HTMLElement, top: number) => {
		const now = performance.now();
		flight.current = { from: flight.current?.from ?? node.scrollTop, to: top, aimedAt: now };
		lastAimAt.current = now;
		lastWritten.current = top;
		node.scrollTo({ top, behavior: "smooth" });
	};
	const flying = () => {
		const current = flight.current;
		if (current && performance.now() - current.aimedAt > FLIGHT_MAX_MS) flight.current = null;
		return flight.current != null;
	};

	const cancel = useCallback(() => {
		flight.current = null;
		if (performance.now() - lastAimAt.current > FLIGHT_MAX_MS) return;
		lastAimAt.current = 0;
		// Stop a native smooth scroll in place. Two writes, because Chromium may skip
		// a write to the offset it already reports instead of cancelling the animation.
		const node = getNode();
		if (!node) return;
		const top = node.scrollTop;
		node.scrollTop = top + 1;
		node.scrollTop = top;
		lastWritten.current = node.scrollTop;
	}, [getNode]);

	/**
	 * Stop following and stay still for `ms`, without deciding yet whether the reader
	 * left the end. Used for wheel and key intent: the scroll it causes (if any) then
	 * decides, so a gesture consumed by a nested scroller never unpins the log.
	 */
	const hold = useCallback((ms: number) => {
		cancel();
		holdUntil.current = performance.now() + ms;
	}, [cancel]);

	/** Record a write made outside this hook (e.g. virtualizer anchoring) as ours. */
	const markWritten = useCallback((top: number) => {
		lastWritten.current = top;
	}, []);

	/** True while a smooth scroll we started is still in flight. */
	const isFlying = useCallback(() => flying(), []);

	/** True when a scroll event at `top` was caused by one of our writes or animations. */
	const isOwnScroll = useCallback((top: number) => {
		const current = flying() ? flight.current : null;
		if (current) {
			const low = Math.min(current.from, current.to) - OWN_SCROLL_PX;
			const high = Math.max(current.from, current.to) + OWN_SCROLL_PX;
			if (top >= low && top <= high) return true;
		}
		return lastWritten.current != null && Math.abs(top - lastWritten.current) < OWN_SCROLL_PX;
	}, []);

	const followEnd = useCallback(() => {
		const node = getNode();
		if (!node || performance.now() < holdUntil.current) return;
		if (snap.current) {
			// The browser clamps to the real maximum; writing scrollHeight is the plain snap.
			write(node, node.scrollHeight);
			return;
		}
		const end = endOf(node);
		if (node.scrollTop > end) {
			// Content shrank below the current position; land exactly, never animate back.
			flight.current = null;
			write(node, end);
			return;
		}
		if (flying()) {
			// Let the scroll in flight land; re-aim only when its target moved a lot.
			if (Math.abs(flight.current!.to - end) > REAIM_PX) smoothTo(node, end);
			return;
		}
		const gap = end - node.scrollTop;
		if (gap <= SETTLE_PX) return;
		if (gap <= FOLLOW_INSTANT_PX) write(node, end);
		else smoothTo(node, end);
	}, [getNode]);
	const followEndRef = useRef(followEnd);
	followEndRef.current = followEnd;

	const glideToEnd = useCallback(() => {
		const node = getNode();
		if (!node) return;
		// A deliberate jump (send, Jump to latest) overrides any pending wheel hold.
		holdUntil.current = 0;
		if (snap.current) {
			flight.current = null;
			write(node, node.scrollHeight);
			return;
		}
		smoothTo(node, endOf(node));
	}, [getNode]);

	/**
	 * Re-issue the smooth scroll in flight at the current end. Call after an instant
	 * write elsewhere would have cancelled it (the browser aborts a smooth scroll on
	 * any instant programmatic scroll).
	 */
	const reaim = useCallback(() => {
		const node = getNode();
		if (node && flying() && !snap.current) smoothTo(node, endOf(node));
	}, [getNode]);

	/**
	 * The browser finished a scroll (wire to the scroller's onScrollEnd). Only a
	 * landing on our target ends the flight: a `scrollend` queued by an earlier
	 * instant write must not clear a fresh one. After landing, catch up with any
	 * growth that arrived meanwhile.
	 */
	const endFlight = useCallback(() => {
		const node = getNode();
		const current = flight.current;
		if (!node || !current) return;
		if (Math.abs(node.scrollTop - current.to) > OWN_SCROLL_PX && node.scrollTop < endOf(node) - OWN_SCROLL_PX) return;
		flight.current = null;
		followEndRef.current();
	}, [getNode]);

	useEffect(() => () => {
		flight.current = null;
	}, []);

	return { glideToEnd, followEnd, cancel, hold, markWritten, isOwnScroll, isFlying, reaim, endFlight };
}
