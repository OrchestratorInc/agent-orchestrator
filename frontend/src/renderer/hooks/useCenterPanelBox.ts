import { useLayoutEffect, useState } from "react";
import type { ViewportRect } from "./useLiveBrowserViewportRect";

const PANEL_SELECTOR = ".center-panel-surface";
const TOPBAR_SELECTOR = ".workspace-topbar-container";

/** The panel's rect and corner radius, and how far a top bar laid out along
 * its top edge reaches into it (0 without one). */
export type CenterPanelBox = ViewportRect & { radius: string; topInset: number };

function sameBox(a: CenterPanelBox | null, b: CenterPanelBox | null): boolean {
	if (a === b) return true;
	if (!a || !b) return false;
	return a.left === b.left && a.top === b.top && a.right === b.right && a.bottom === b.bottom && a.radius === b.radius && a.topInset === b.topInset;
}

/**
 * The visible framed center panel's window rect, corner radius and top-bar
 * inset, or null while none is laid out. Floating content that belongs to the panel uses it
 * to sit inside the panel and be clipped by its edge, without being rendered
 * inside it (Electron drag regions follow DOM order).
 */
export function useCenterPanelBox(): CenterPanelBox | null {
	const [box, setBox] = useState<CenterPanelBox | null>(null);
	useLayoutEffect(() => {
		let observed: Element[] = [];
		let frame = 0;
		const measure = () => {
			let next: CenterPanelBox | null = null;
			for (const panel of document.querySelectorAll(PANEL_SELECTOR)) {
				const rect = panel.getBoundingClientRect();
				if (rect.width === 0 || rect.height === 0) continue;
				next = { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom, radius: getComputedStyle(panel).borderRadius, topInset: 0 };
			}
			if (next) {
				for (const bar of document.querySelectorAll(TOPBAR_SELECTOR)) {
					const rect = bar.getBoundingClientRect();
					// Only a bar laid out across the panel's top edge counts.
					if (rect.height === 0 || rect.bottom <= next.top || rect.top >= next.top + (next.bottom - next.top) / 2 || rect.right <= next.left || rect.left >= next.right) continue;
					next.topInset = Math.max(next.topInset, Math.round(rect.bottom - next.top));
				}
			}
			setBox((current) => (sameBox(current, next) ? current : next));
		};
		const resizeObserver = new ResizeObserver(measure);
		// Panels and bars mount and unmount with routes; only a changed set is observed.
		const sync = () => {
			frame = 0;
			const found = [...document.querySelectorAll(`${PANEL_SELECTOR}, ${TOPBAR_SELECTOR}`)];
			if (found.length === observed.length && found.every((element, index) => element === observed[index])) return;
			resizeObserver.disconnect();
			observed = found;
			for (const element of observed) resizeObserver.observe(element);
			measure();
		};
		const scheduleSync = () => {
			if (!frame) frame = requestAnimationFrame(sync);
		};
		sync();
		const mutationObserver = new MutationObserver(scheduleSync);
		mutationObserver.observe(document.body, { childList: true, subtree: true });
		window.addEventListener("resize", measure);
		// The shell animates its inset (sidebar, fullscreen), which moves a panel
		// without resizing it.
		document.addEventListener("transitionend", measure);
		return () => {
			if (frame) cancelAnimationFrame(frame);
			mutationObserver.disconnect();
			resizeObserver.disconnect();
			window.removeEventListener("resize", measure);
			document.removeEventListener("transitionend", measure);
		};
	}, []);
	return box;
}
