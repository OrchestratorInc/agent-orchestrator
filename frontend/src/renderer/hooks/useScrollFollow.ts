import { animate } from "motion";
import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef } from "react";

/** Ease-out with a long, soft landing (whirl's send glide uses the same curve). */
const GLIDE_EASE = [0.22, 0.61, 0.36, 1] as const;
/** Distance-scaled glide: short hops stay quick, long ones never drag. */
const GLIDE_PX_PER_SECOND = 1600;
const GLIDE_MIN_SECONDS = 0.35;
const GLIDE_MAX_SECONDS = 0.6;
/** Follow time constant: how quickly streamed growth is caught up with. */
const FOLLOW_TAU_MS = 90;
/** Writes this close to the current position are skipped. */
const SETTLE_PX = 0.5;
/** Scroll events within this window after our own write are ours, not the reader's. */
const PROGRAMMATIC_WINDOW_MS = 120;

/**
 * Tests snap, as under reduced motion, because they assert scroll positions
 * synchronously and jsdom has no layout to animate. The test-mode branch is
 * compiled out of production builds.
 */
const CANNOT_ANIMATE = typeof requestAnimationFrame !== "function" || import.meta.env.MODE === "test";

export function glideSeconds(distance: number): number {
	return Math.min(GLIDE_MAX_SECONDS, Math.max(GLIDE_MIN_SECONDS, Math.abs(distance) / GLIDE_PX_PER_SECOND));
}

/**
 * Moves a scroll viewport toward its end without snapping.
 *
 * `glideToEnd` is for a deliberate jump, such as the reader sending a message: an
 * eased tween whose target is re-read every frame, so content that grows or a
 * spacer that resizes mid-flight retargets the tween instead of cancelling it.
 * `followEnd` is for streamed growth: it closes the gap with an exponential
 * catch-up, which keeps velocity continuous when chunks land unevenly. Both stop
 * the moment `cancel` is called (on real reader input), and both snap instead of
 * animating under reduced motion.
 */
export function useScrollFollow(getNode: () => HTMLElement | null) {
	const reducedMotion = useReducedMotion();
	const snap = useRef(false);
	snap.current = CANNOT_ANIMATE || Boolean(reducedMotion);
	const glide = useRef<{ stop: () => void } | null>(null);
	const frame = useRef<number | null>(null);
	const lastWrite = useRef(0);

	const endOf = (node: HTMLElement) => Math.max(0, node.scrollHeight - node.clientHeight);
	const write = (node: HTMLElement, top: number) => {
		if (Math.abs(node.scrollTop - top) <= SETTLE_PX) return;
		lastWrite.current = performance.now();
		node.scrollTop = top;
	};

	const cancel = useCallback(() => {
		glide.current?.stop();
		glide.current = null;
		if (frame.current != null) cancelAnimationFrame(frame.current);
		frame.current = null;
		// The reader has taken over: their next scroll event must not read as ours.
		lastWrite.current = 0;
	}, []);

	const followEnd = useCallback(() => {
		const node = getNode();
		if (!node || glide.current || frame.current != null) return;
		if (snap.current) {
			// The browser clamps to the real maximum; writing scrollHeight is the plain snap.
			write(node, node.scrollHeight);
			return;
		}
		let previous = performance.now();
		const step = (now: number) => {
			const current = getNode();
			if (!current) {
				frame.current = null;
				return;
			}
			const target = endOf(current);
			const gap = target - current.scrollTop;
			if (gap <= SETTLE_PX) {
				// Content may also have shrunk below the current position; land exactly.
				write(current, target);
				frame.current = null;
				return;
			}
			const blend = 1 - Math.exp(-(now - previous) / FOLLOW_TAU_MS);
			previous = now;
			// At least 1px per frame so a small gap can't fall under SETTLE_PX and stall.
			const advance = Math.max(gap * blend, Math.min(gap, 1));
			if (gap - advance <= 1) {
				write(current, target);
				frame.current = null;
				return;
			}
			write(current, current.scrollTop + advance);
			frame.current = requestAnimationFrame(step);
		};
		frame.current = requestAnimationFrame(step);
	}, [getNode]);

	const glideToEnd = useCallback(() => {
		const node = getNode();
		if (!node) return;
		cancel();
		const from = node.scrollTop;
		if (snap.current) {
			// The browser clamps to the real maximum; writing scrollHeight is the plain snap.
			write(node, node.scrollHeight);
			return;
		}
		glide.current = animate(0, 1, {
			duration: glideSeconds(endOf(node) - from),
			ease: GLIDE_EASE,
			onUpdate: (progress) => {
				const current = getNode();
				if (current) write(current, from + (endOf(current) - from) * progress);
			},
			onComplete: () => {
				glide.current = null;
				// Anything that landed during the glide is caught up without a snap.
				followEnd();
			},
		});
	}, [cancel, followEnd, getNode]);

	/** True while a scroll event most likely came from our own write. */
	const isProgrammaticScroll = useCallback(
		() => glide.current != null || performance.now() - lastWrite.current < PROGRAMMATIC_WINDOW_MS,
		[],
	);

	useEffect(() => cancel, [cancel]);

	return { glideToEnd, followEnd, cancel, isProgrammaticScroll };
}
