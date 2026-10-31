import { useLayoutEffect, useState, type RefObject } from "react";

/** Follow the selection's first line without changing the reader's scroll. */
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
		const update = () => {
			frame = undefined;
			const selection = window.getSelection();
			const current = selection?.rangeCount ? selection.getRangeAt(0) : undefined;
			if (!source?.isConnected || !viewport.contains(source) || !current || selection?.isCollapsed ||
				current.startContainer !== range.startContainer || current.startOffset !== range.startOffset ||
				current.endContainer !== range.endContainer || current.endOffset !== range.endOffset) {
				onInvalid();
				return;
			}
			const rect = Array.from(range.getClientRects()).find((line) => line.width > 0 && line.height > 0)
				?? range.getBoundingClientRect();
			const bounds = viewport.getBoundingClientRect();
			const parent = button.offsetParent?.getBoundingClientRect() ?? bounds;
			const size = button.getBoundingClientRect();
			const left = Math.max(bounds.left + 8, Math.min(
				rect.left + rect.width / 2 - size.width / 2, bounds.right - size.width - 8,
			)) - parent.left;
			const top = rect.top - parent.top - size.height - 6;
			const visible = rect.bottom > bounds.top && rect.top < bounds.bottom &&
				rect.right > bounds.left && rect.left < bounds.right;
			setPosition((previous) => previous?.left === left && previous.top === top && previous.visible === visible
				? previous : { left, top, visible });
		};
		const schedule = () => { frame ??= window.requestAnimationFrame(update); };
		const resize = new ResizeObserver(schedule);
		resize.observe(viewport);
		resize.observe(source);
		resize.observe(button);
		const mutation = new MutationObserver(schedule);
		mutation.observe(viewport, { subtree: true, childList: true, characterData: true, attributes: true });
		viewport.addEventListener("scroll", schedule, true);
		window.addEventListener("resize", schedule);
		document.addEventListener("selectionchange", schedule);
		update();
		return () => {
			if (frame !== undefined) window.cancelAnimationFrame(frame);
			resize.disconnect();
			mutation.disconnect();
			viewport.removeEventListener("scroll", schedule, true);
			window.removeEventListener("resize", schedule);
			document.removeEventListener("selectionchange", schedule);
		};
	}, [range, viewportRef, buttonRef, onInvalid]);
	return position;
}
