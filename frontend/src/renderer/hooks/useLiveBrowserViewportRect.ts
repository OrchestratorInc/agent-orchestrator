import { useLayoutEffect, useState } from "react";

const LIVE_VIEWPORT_SELECTOR = '.browser-panel[data-browser-native-page="live"] .browser-panel__viewport';

export type ViewportRect = { left: number; top: number; right: number; bottom: number };

function sameRect(a: ViewportRect | null, b: ViewportRect | null): boolean {
	if (a === b) return true;
	if (!a || !b) return false;
	return a.left === b.left && a.top === b.top && a.right === b.right && a.bottom === b.bottom;
}

/**
 * The window rect the live native browser page covers, or null while none is
 * shown. The native WebContentsView is sized to exactly this element, so a
 * non-modal surface that stays outside it never needs the shell raised over the
 * page (which would swallow every click meant for the browser).
 */
export function useLiveBrowserViewportRect(): ViewportRect | null {
	const [rect, setRect] = useState<ViewportRect | null>(null);
	useLayoutEffect(() => {
		let viewports: Element[] = [];
		let frame = 0;
		const measure = () => {
			let next: ViewportRect | null = null;
			for (const viewport of viewports) {
				const box = viewport.getBoundingClientRect();
				// Inactive panels stay mounted but are not laid out.
				if (box.width <= 0 || box.height <= 0) continue;
				next = next
					? { left: Math.min(next.left, box.left), top: Math.min(next.top, box.top), right: Math.max(next.right, box.right), bottom: Math.max(next.bottom, box.bottom) }
					: { left: box.left, top: box.top, right: box.right, bottom: box.bottom };
			}
			setRect((current) => (sameRect(current, next) ? current : next));
		};
		const resizeObserver = new ResizeObserver(measure);
		const sync = () => {
			frame = 0;
			const found = [...document.querySelectorAll(LIVE_VIEWPORT_SELECTOR)];
			if (found.length !== viewports.length || found.some((viewport, index) => viewport !== viewports[index])) {
				resizeObserver.disconnect();
				viewports = found;
				for (const viewport of viewports) resizeObserver.observe(viewport);
			}
			// Maximize/restore and inspector tab switches move the page without
			// necessarily resizing it, so always re-measure.
			measure();
		};
		const scheduleSync = () => {
			if (!frame) frame = requestAnimationFrame(sync);
		};
		sync();
		const mutationObserver = new MutationObserver(scheduleSync);
		mutationObserver.observe(document.body, {
			childList: true,
			subtree: true,
			attributes: true,
			attributeFilter: ["data-browser-native-page"],
		});
		window.addEventListener("resize", scheduleSync);
		document.addEventListener("transitionend", scheduleSync);
		return () => {
			if (frame) cancelAnimationFrame(frame);
			mutationObserver.disconnect();
			resizeObserver.disconnect();
			window.removeEventListener("resize", scheduleSync);
			document.removeEventListener("transitionend", scheduleSync);
		};
	}, []);
	return rect;
}
