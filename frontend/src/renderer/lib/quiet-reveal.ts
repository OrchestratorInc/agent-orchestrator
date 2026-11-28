import { visualElementStore, type ResolvedValues, type VisualElement } from "motion/react";
import { useLayoutEffect, useRef, type RefObject } from "react";

/**
 * A view kept alive under `<Activity>` is hidden with `display: none`, and a
 * kept view's top-bar container is taken out of the document. When it is shown
 * again the browser starts every CSS animation in it from the beginning: a sent
 * message's entrance, an open fold's slide, the transcript's fade, the header's
 * arrival. Those already played. Finishing them before the frame paints brings
 * the view back as it was left. Looping animations (spinners, shimmers) are
 * left running.
 */
export function finishReplayedAnimations(root: Element): void {
	if (typeof root.getAnimations !== "function" || typeof CSSAnimation === "undefined") return;
	for (const animation of root.getAnimations({ subtree: true })) {
		if (!(animation instanceof CSSAnimation) || animation.effect?.getTiming().iterations === Infinity) continue;
		try {
			animation.finish();
		} catch {
			// One that cannot finish (zero playback rate) is left as it is.
		}
	}
}

/**
 * Finishes them in the next animation frame, which runs before that frame's
 * paint: the style pass it needs is the one the frame does anyway, not an
 * extra one forced in the middle of a commit. Returns a cancel.
 */
export function finishReplayedAnimationsBeforePaint(root: Element): () => void {
	const frame = requestAnimationFrame(() => finishReplayedAnimations(root));
	return () => cancelAnimationFrame(frame);
}

type HeldMotion = Map<VisualElement, ResolvedValues>;

/**
 * Hiding a view detaches its refs and showing it attaches them again, which
 * Motion treats as a remount: every element mounted with an `initial` jumps back
 * to it and animates in again (a row that arrived while the reader watched, a
 * fold they opened). Taken as the view hides, while its elements are still
 * mounted, these are the values to put back once they mount again.
 */
function holdMotionValues(root: Element): HeldMotion {
	const held: HeldMotion = new Map();
	for (const element of [root, ...root.querySelectorAll("*")]) {
		const visual = visualElementStore.get(element);
		if (visual) held.set(visual, { ...visual.latestValues });
	}
	return held;
}

/** Puts back what Motion reset as the view mounted again; its own animation then has nowhere to go. */
function restoreMotionValues(held: HeldMotion): void {
	for (const [visual, values] of held) {
		if (!visual.current) continue;
		for (const key in values) {
			const value = values[key];
			if (value === undefined || visual.latestValues[key] === value) continue;
			visual.getValue(key)?.jump(value);
			visual.latestValues[key] = value;
		}
	}
}

/**
 * Brings a kept view back as it was left: no CSS entrance (finishReplayedAnimations)
 * and no Motion entrance (holdMotionValues) plays again. A layout effect with
 * stable inputs runs again only when a hidden `<Activity>` (or Suspense
 * boundary) shows its view, after the view's own refs and layout effects, and
 * before that frame paints; its cleanup runs as the view hides, before the
 * view's refs detach. The cleanup also runs on unmount, where what it holds is
 * simply dropped: telling the two apart would cost a style pass.
 */
export function useQuietReveal(ref: RefObject<Element | null>): void {
	const shownBefore = useRef(false);
	const held = useRef<HeldMotion | undefined>(undefined);
	useLayoutEffect(() => {
		// Read as each runs, not when the view first mounted: it may have shown a
		// loading state then, with no element yet.
		const root = ref.current;
		let cancelFinish: (() => void) | undefined;
		if (shownBefore.current && root) {
			if (held.current) restoreMotionValues(held.current);
			cancelFinish = finishReplayedAnimationsBeforePaint(root);
		}
		held.current = undefined;
		shownBefore.current = true;
		return () => {
			cancelFinish?.();
			const hiding = ref.current;
			if (hiding) held.current = holdMotionValues(hiding);
		};
	}, [ref]);
}
