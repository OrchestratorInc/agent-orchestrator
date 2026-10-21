import { useLayoutEffect, useState } from "react";

const TOPBAR_SELECTOR = ".workspace-topbar-container";

/**
 * The viewport y where the visible workspace top bar ends, or null while none
 * is mounted. Every surface's bar (the shell topbar, session actions, Artifacts,
 * Automations, …) carries `.workspace-topbar-container`. Where it ends depends
 * on Windows' titlebar, the framed center panel's per-platform inset, Linux's
 * session drag strip and macOS fullscreen, so it is measured, not rebuilt from
 * tokens.
 */
export function useWorkspaceTopbarBottom(): number | null {
	const [bottom, setBottom] = useState<number | null>(null);
	useLayoutEffect(() => {
		let bars: Element[] = [];
		let frame = 0;
		const measure = () => {
			let next: number | null = null;
			for (const bar of bars) {
				const rect = bar.getBoundingClientRect();
				// Only bars along the window's top edge that are actually laid out.
				if (rect.height > 0 && rect.top < window.innerHeight / 2) next = Math.max(next ?? 0, rect.bottom);
			}
			setBottom(next);
		};
		const resizeObserver = new ResizeObserver(measure);
		// Bars mount and unmount with routes and Settings. Querying is cheap; only
		// a changed set is observed and measured, so DOM churn forces no layout.
		const sync = () => {
			frame = 0;
			const found = [...document.querySelectorAll(TOPBAR_SELECTOR)];
			if (found.length === bars.length && found.every((bar, index) => bar === bars[index])) return;
			resizeObserver.disconnect();
			bars = found;
			for (const bar of bars) resizeObserver.observe(bar);
			measure();
		};
		const scheduleSync = () => {
			if (!frame) frame = requestAnimationFrame(sync);
		};
		// The center panel animates its inset (macOS fullscreen), which moves a bar
		// without resizing it.
		const onTransitionEnd = (event: TransitionEvent) => {
			const target = event.target;
			if (target instanceof Node && bars.some((bar) => target.contains(bar))) measure();
		};
		sync();
		const mutationObserver = new MutationObserver(scheduleSync);
		mutationObserver.observe(document.body, { childList: true, subtree: true });
		window.addEventListener("resize", measure);
		document.addEventListener("transitionend", onTransitionEnd);
		return () => {
			if (frame) cancelAnimationFrame(frame);
			mutationObserver.disconnect();
			resizeObserver.disconnect();
			window.removeEventListener("resize", measure);
			document.removeEventListener("transitionend", onTransitionEnd);
		};
	}, []);
	return bottom;
}
