import { useLayoutEffect, useState, useSyncExternalStore } from "react";
import type { ViewportRect } from "./useLiveBrowserViewportRect";

/**
 * The window rect the harness update notice (pinned to the center panel's top-right
 * corner) covers, or null while it is not shown. The notice publishes it, so
 * session content that sits in that corner can keep its own controls clear of
 * the notice instead of the notice moving over them.
 */
let noticeRect: ViewportRect | null = null;
const listeners = new Set<() => void>();

function sameRect(a: ViewportRect | null, b: ViewportRect | null): boolean {
	if (a === b) return true;
	if (!a || !b) return false;
	return a.left === b.left && a.top === b.top && a.right === b.right && a.bottom === b.bottom;
}

export function publishHarnessUpdateNoticeRect(rect: ViewportRect | null): void {
	if (sameRect(noticeRect, rect)) return;
	noticeRect = rect;
	for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

const readNoticeRect = () => noticeRect;
const readNoNotice = () => null;

/** The notice's current window rect, or null while it is not shown. */
export function useHarnessUpdateNoticeRect(): ViewportRect | null {
	return useSyncExternalStore(subscribe, readNoticeRect, readNoNotice);
}

/** Space kept between the notice and the content it would otherwise cover. */
const CLEARANCE_GAP = 12; // --space-3

/**
 * How far `element` reaches under the notice, counting the gap: the right inset
 * it needs so nothing of it sits beneath the notice. 0 when they do not
 * overlap. Never more than half the element, so a narrow window still leaves
 * the content most of its room.
 */
export function harnessUpdateNoticeIntrusion(element: ViewportRect, notice: ViewportRect | null): number {
	if (!notice) return 0;
	const edge = notice.left - CLEARANCE_GAP;
	const overlapsX = element.right > edge && element.left < notice.right;
	const overlapsY = element.top < notice.bottom && element.bottom > notice.top;
	if (!overlapsX || !overlapsY) return 0;
	return Math.ceil(Math.min(element.right - edge, (element.right - element.left) / 2));
}

/**
 * The right inset `element` needs to keep clear of the harness update notice,
 * re-measured as it resizes, the window resizes, or anything scrolls (a chat
 * timeline moves its content under the fixed notice). Listens only while the
 * notice is shown.
 *
 * `contentBox`, when given, returns where the content that must stay clear
 * would sit inside `element` without any inset; the inset then applies only
 * while that box actually reaches under the notice. Keep it stable.
 */
export function useHarnessUpdateNoticeClearance(
	element: HTMLElement | null,
	contentBox?: (element: HTMLElement) => ViewportRect | null,
): number {
	const notice = useHarnessUpdateNoticeRect();
	const [clearance, setClearance] = useState(0);
	useLayoutEffect(() => {
		if (!notice || !element) {
			setClearance(0);
			return;
		}
		let frame = 0;
		const measure = () => {
			frame = 0;
			const box = element.getBoundingClientRect();
			const content = contentBox ? contentBox(element) : box;
			const covered = box.width > 0 && content !== null && harnessUpdateNoticeIntrusion(content, notice) > 0;
			setClearance(covered ? harnessUpdateNoticeIntrusion(box, notice) : 0);
		};
		const schedule = () => {
			if (!frame) frame = requestAnimationFrame(measure);
		};
		measure();
		const resizeObserver = new ResizeObserver(schedule);
		resizeObserver.observe(element);
		window.addEventListener("resize", schedule);
		document.addEventListener("scroll", schedule, { capture: true, passive: true });
		return () => {
			if (frame) cancelAnimationFrame(frame);
			resizeObserver.disconnect();
			window.removeEventListener("resize", schedule);
			document.removeEventListener("scroll", schedule, { capture: true });
		};
	}, [contentBox, element, notice]);
	return clearance;
}
