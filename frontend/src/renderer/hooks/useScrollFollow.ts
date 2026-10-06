import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef } from "react";

/** Writes this close to the current position are skipped. */
const SETTLE_PX = 0.5;
/** A scroll event within this distance of our last written position is ours. */
const OWN_SCROLL_PX = 2;
/** A native smooth scroll is long over by now even if its end event was missed. */
const FLIGHT_MAX_MS = 1500;

/**
 * Tests snap, as under reduced motion, because they assert scroll positions
 * synchronously and jsdom has no layout to animate. The test-mode branch is
 * compiled out of production builds.
 */
const CANNOT_ANIMATE = import.meta.env.MODE === "test";

/**
 * Moves a scroll viewport toward its end without snapping.
 *
 * The motion itself is the browser's native smooth scroll, which Chromium runs on
 * the compositor: it keeps moving while the main thread is busy committing the
 * send or a streamed chunk, where a script-driven tween would stall and stutter.
 * `glideToEnd` starts one for a deliberate jump (send, Jump to latest).
 * `followEnd` keeps streamed growth in view: it starts one when the end moves
 * away, and re-aims the one in flight when the end moves again. Reader input
 * stops it on the spot (`cancel` / `hold`). Reduced motion snaps instead.
 */
export function useScrollFollow(getNode: () => HTMLElement | null) {
	const reducedMotion = useReducedMotion();
	const snap = useRef(false);
	snap.current = CANNOT_ANIMATE || Boolean(reducedMotion);
	// Where our last write left (or will leave) the viewport. Scroll events are
	// classified by position rather than by time: an event that lands here is ours
	// however late it arrives, and anything else is the reader's.
	const lastWritten = useRef<number | null>(null);
	// The native smooth scroll in flight, if any. Every position along its path is ours.
	const flight = useRef<{ from: number; to: number; startedAt: number } | null>(null);
	const holdUntil = useRef(0);

	const endOf = (node: HTMLElement) => Math.max(0, node.scrollHeight - node.clientHeight);
	const write = (node: HTMLElement, top: number) => {
		if (Math.abs(node.scrollTop - top) <= SETTLE_PX) return;
		node.scrollTop = top;
		lastWritten.current = node.scrollTop;
	};
	const smoothTo = (node: HTMLElement, top: number) => {
		flight.current = {
			from: flight.current?.from ?? node.scrollTop,
			to: top,
			startedAt: flight.current?.startedAt ?? performance.now(),
		};
		lastWritten.current = top;
		node.scrollTo({ top, behavior: "smooth" });
	};

	const cancel = useCallback(() => {
		if (!flight.current) return;
		flight.current = null;
		// An instant scroll to where the animation is now stops it without a jump.
		const node = getNode();
		if (node) {
			node.scrollTo({ top: node.scrollTop });
			lastWritten.current = node.scrollTop;
		}
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

	/** True when a scroll event at `top` was caused by one of our writes or animations. */
	const isOwnScroll = useCallback((top: number) => {
		const current = flight.current;
		if (current && performance.now() - current.startedAt > FLIGHT_MAX_MS) flight.current = null;
		else if (current) {
			if (Math.abs(top - current.to) < OWN_SCROLL_PX) {
				flight.current = null;
				return true;
			}
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
		if (flight.current) {
			// Re-aim the scroll in flight at the new end instead of restarting it.
			if (Math.abs(flight.current.to - end) > SETTLE_PX) smoothTo(node, end);
			return;
		}
		if (node.scrollTop > end) {
			// Content shrank below the current position; land exactly.
			write(node, end);
			return;
		}
		if (end - node.scrollTop > SETTLE_PX) smoothTo(node, end);
	}, [getNode]);

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

	/** The browser finished a scroll (wire to the scroller's onScrollEnd). */
	const endFlight = useCallback(() => {
		flight.current = null;
	}, []);

	useEffect(() => () => {
		flight.current = null;
	}, []);

	return { glideToEnd, followEnd, cancel, hold, markWritten, isOwnScroll, endFlight };
}
