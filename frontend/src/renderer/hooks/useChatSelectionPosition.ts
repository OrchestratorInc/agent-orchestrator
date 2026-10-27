import { useLayoutEffect, useState, type RefObject } from "react";
import { selectionRangesEqual, selectionToolbarPosition, selectionViewport } from "../lib/selection-toolbar";

/** Follow the visible selection without changing the reader's scroll. */
export function useChatSelectionPosition(
	range: Range | undefined,
	viewportRef: RefObject<HTMLDivElement | null>,
	buttonRef: RefObject<HTMLDivElement | null>,
	onInvalid: () => void,
) {
	const [position, setPosition] = useState<{ left: number; top: number; visible: boolean }>();
	useLayoutEffect(() => {
		const viewport = viewportRef.current;
		const button = buttonRef.current;
		if (!range || !viewport || !button) return;
		const anchor = range.startContainer instanceof Element ? range.startContainer : range.startContainer.parentElement;
		const source = anchor?.closest("[data-chat-message-id]");
		if (!source) {
			onInvalid();
			return;
		}
		let frame: number | undefined;
		let disposed = false;
		const update = () => {
			if (disposed) return;
			frame = undefined;
			const selection = window.getSelection();
			const current = selection?.rangeCount ? selection.getRangeAt(0) : undefined;
			const toolbarFocused = button.contains(document.activeElement);
			const matches = !selection?.isCollapsed && selectionRangesEqual(current, range);
			if (!source?.isConnected || !viewport.contains(source) || !range.startContainer.isConnected ||
				(!matches && !(toolbarFocused && (!current || selection?.isCollapsed)))) {
				onInvalid();
				return;
			}
			const rect = range.getBoundingClientRect();
			const bounds = selectionViewport(viewport);
			const parent = button.offsetParent?.getBoundingClientRect() ?? bounds;
			const maxWidth = `${Math.max(0, bounds.right - bounds.left - 16)}px`;
			if (button.style.maxWidth !== maxWidth) button.style.maxWidth = maxWidth;
			const size = button.getBoundingClientRect();
			const placed = selectionToolbarPosition(rect, bounds, size);
			const left = placed.left - parent.left;
			const top = placed.top - parent.top;
			const visible = placed.visible;
			setPosition((previous) => previous?.left === left && previous.top === top && previous.visible === visible
				? previous : { left, top, visible });
		};
		const schedule = () => { if (!disposed) frame ??= window.requestAnimationFrame(update); };
		const resize = new ResizeObserver(schedule);
		resize.observe(viewport);
		resize.observe(source);
		resize.observe(button);
		const mutation = new MutationObserver(schedule);
		mutation.observe(source, { subtree: true, childList: true, characterData: true });
		mutation.observe(viewport, { subtree: true, childList: true });
		document.addEventListener("scroll", schedule, true);
		window.addEventListener("resize", schedule);
		window.visualViewport?.addEventListener("resize", schedule);
		window.visualViewport?.addEventListener("scroll", schedule);
		document.addEventListener("selectionchange", schedule);
		update();
		return () => {
			disposed = true;
			if (frame !== undefined) window.cancelAnimationFrame(frame);
			resize.disconnect();
			mutation.disconnect();
			document.removeEventListener("scroll", schedule, true);
			window.removeEventListener("resize", schedule);
			window.visualViewport?.removeEventListener("resize", schedule);
			window.visualViewport?.removeEventListener("scroll", schedule);
			document.removeEventListener("selectionchange", schedule);
		};
	}, [range, viewportRef, buttonRef, onInvalid]);
	return position;
}
