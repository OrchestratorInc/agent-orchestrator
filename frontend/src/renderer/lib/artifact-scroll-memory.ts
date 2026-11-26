import { createContext, useContext, useLayoutEffect } from "react";
import type { ScrollPosition } from "../stores/ui-store";

/** Marks the element an artifact view scrolls in: the one whose position is remembered and put back. */
export const ARTIFACT_SCROLLER = "[data-artifact-scroll]";

// A position not reached by then is not coming: the content is shorter than it was.
const RESTORE_WINDOW_MS = 3_000;

// The reader taking over: a restore still on its way stops.
const READER_INPUTS = ["wheel", "pointerdown", "keydown", "touchstart"] as const;

/**
 * Where the open artifact, or the Artifacts list, is scrolled, kept while the
 * view is shown so it can be remembered when the view goes, and the position
 * it is to go back to when it is shown again. A restore is pending until the
 * view reaches it, the reader scrolls first, or it is given up.
 */
export class ArtifactScrollMemory {
	private pending: ScrollPosition | undefined;
	private position: ScrollPosition | undefined;

	constructor(restore?: ScrollPosition) {
		this.pending = restore;
		this.position = restore;
	}

	/** The position to go back to, while a restore is pending. */
	pendingPosition(): ScrollPosition | undefined {
		return this.pending;
	}

	/** Nothing is left to restore: the position was reached, or the reader moved first. */
	settle() {
		this.pending = undefined;
	}

	/** The view scrolled. Ignored while a restore is pending: those are the view laying out on its way there. */
	record(position: ScrollPosition) {
		if (!this.pending) this.position = position;
	}

	/** Where the view is, or, while a restore is pending, where it is going back to. */
	current(): ScrollPosition | undefined {
		return this.position;
	}

	/** Another file or view is shown: nothing to restore, nothing remembered. */
	clear() {
		this.pending = undefined;
		this.position = undefined;
	}
}

/** The open artifact's scroll memory, for a framed page that scrolls itself; null elsewhere. */
export const ArtifactScrollMemoryContext = createContext<ArtifactScrollMemory | null>(null);

export function useArtifactScrollMemory(): ArtifactScrollMemory | null {
	return useContext(ArtifactScrollMemoryContext);
}

/**
 * Keeps `memory` up to date with the scroller inside `root` (the element
 * marked data-artifact-scroll, or root itself), and scrolls it back to the
 * memory's pending position: at once when its content is there, or on each
 * frame as the content loads and lays out, until the position is reached,
 * the reader scrolls or presses, or a few seconds pass.
 */
export function useArtifactScrollRestore(root: HTMLElement | null, memory: ArtifactScrollMemory, enabled: boolean) {
	useLayoutEffect(() => {
		if (!root || !enabled) return;
		const scroller = () => (root.matches(ARTIFACT_SCROLLER) ? root : root.querySelector<HTMLElement>(ARTIFACT_SCROLLER));
		const onScroll = (event: Event) => {
			const target = event.target;
			if (!(target instanceof HTMLElement) || !target.matches(ARTIFACT_SCROLLER)) return;
			memory.record({ x: target.scrollLeft, y: target.scrollTop });
		};
		let frame = 0;
		const removeInputs = () => {
			for (const name of READER_INPUTS) root.removeEventListener(name, stop, true);
		};
		// Settled, the memory holds where the view really is: short of the
		// position when the content no longer reaches it, or wherever the reader
		// took over.
		const stop = () => {
			window.cancelAnimationFrame(frame);
			memory.settle();
			removeInputs();
			const element = scroller();
			if (element) memory.record({ x: element.scrollLeft, y: element.scrollTop });
		};
		const started = performance.now();
		const restore = () => {
			const target = memory.pendingPosition();
			if (!target) return removeInputs();
			const element = scroller();
			if (element) {
				element.scrollLeft = target.x;
				element.scrollTop = target.y;
				if (Math.abs(element.scrollLeft - target.x) <= 1 && Math.abs(element.scrollTop - target.y) <= 1) return stop();
			}
			if (performance.now() - started > RESTORE_WINDOW_MS) return stop();
			frame = window.requestAnimationFrame(restore);
		};
		root.addEventListener("scroll", onScroll, true);
		if (memory.pendingPosition()) {
			for (const name of READER_INPUTS) root.addEventListener(name, stop, { capture: true, passive: true });
		}
		restore();
		return () => {
			window.cancelAnimationFrame(frame);
			root.removeEventListener("scroll", onScroll, true);
			removeInputs();
		};
	}, [enabled, memory, root]);
}
