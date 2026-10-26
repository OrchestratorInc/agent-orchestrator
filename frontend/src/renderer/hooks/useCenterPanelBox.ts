import { useLayoutEffect, useState } from "react";
import type { ViewportRect } from "./useLiveBrowserViewportRect";

const PANEL_SELECTOR = ".center-panel-surface";

export type CenterPanelBox = ViewportRect & { radius: string };

function sameBox(a: CenterPanelBox | null, b: CenterPanelBox | null): boolean {
	if (a === b) return true;
	if (!a || !b) return false;
	return a.left === b.left && a.top === b.top && a.right === b.right && a.bottom === b.bottom && a.radius === b.radius;
}

/**
 * The visible framed center panel's window rect and corner radius, or null
 * while none is laid out. Floating content that belongs to the panel uses it
 * to sit inside the panel and be clipped by its edge, without being rendered
 * inside it (Electron drag regions follow DOM order).
 */
export function useCenterPanelBox(): CenterPanelBox | null {
	const [box, setBox] = useState<CenterPanelBox | null>(null);
	useLayoutEffect(() => {
		let panels: Element[] = [];
		let frame = 0;
		const measure = () => {
			let next: CenterPanelBox | null = null;
			for (const panel of panels) {
				const rect = panel.getBoundingClientRect();
				if (rect.width === 0 || rect.height === 0) continue;
				next = { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom, radius: getComputedStyle(panel).borderRadius };
			}
			setBox((current) => (sameBox(current, next) ? current : next));
		};
		const resizeObserver = new ResizeObserver(measure);
		// Panels mount and unmount with routes; only a changed set is observed.
		const sync = () => {
			frame = 0;
			const found = [...document.querySelectorAll(PANEL_SELECTOR)];
			if (found.length === panels.length && found.every((panel, index) => panel === panels[index])) return;
			resizeObserver.disconnect();
			panels = found;
			for (const panel of panels) resizeObserver.observe(panel);
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
