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
			const lines = range.getClientRects();
			let rect = range.getBoundingClientRect();
			const extent = rect;
			for (let i = 0; i < lines.length; i++) {
				if (lines[i].width > 0 && lines[i].height > 0) { rect = lines[i]; break; }
			}
			const visual = window.visualViewport;
			const bounds = { left: visual?.offsetLeft ?? 0, top: visual?.offsetTop ?? 0,
				right: (visual?.offsetLeft ?? 0) + (visual?.width ?? innerWidth),
				bottom: (visual?.offsetTop ?? 0) + (visual?.height ?? innerHeight) };
			for (let node: HTMLElement | null = viewport; node; node = node.parentElement) {
				const style = getComputedStyle(node);
				const box = node.getBoundingClientRect();
				if (node === viewport || /(auto|scroll|hidden|clip)/.test(style.overflowX)) {
					bounds.left = Math.max(bounds.left, box.left); bounds.right = Math.min(bounds.right, box.right);
				}
				if (node === viewport || /(auto|scroll|hidden|clip)/.test(style.overflowY)) {
					bounds.top = Math.max(bounds.top, box.top); bounds.bottom = Math.min(bounds.bottom, box.bottom);
				}
			}
			const parent = button.offsetParent?.getBoundingClientRect() ?? bounds;
			const size = button.getBoundingClientRect();
			const left = Math.max(bounds.left + 8, Math.min(
				rect.left + rect.width / 2 - size.width / 2, bounds.right - size.width - 8,
			)) - parent.left;
			const top = Math.max(bounds.top + 8, Math.min(rect.top - size.height - 6,
				Math.max(bounds.top + 8, bounds.bottom - size.height - 8))) - parent.top;
			const visible = bounds.bottom > bounds.top && bounds.right > bounds.left &&
				extent.bottom > bounds.top && extent.top < bounds.bottom &&
				extent.right > bounds.left && extent.left < bounds.right;
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
		document.addEventListener("scroll", schedule, true);
		window.visualViewport?.addEventListener("resize", schedule);
		window.visualViewport?.addEventListener("scroll", schedule);
		window.addEventListener("resize", schedule);
		document.addEventListener("selectionchange", schedule);
		update();
		return () => {
			if (frame !== undefined) window.cancelAnimationFrame(frame);
			resize.disconnect();
			mutation.disconnect();
			document.removeEventListener("scroll", schedule, true);
			window.visualViewport?.removeEventListener("resize", schedule);
			window.visualViewport?.removeEventListener("scroll", schedule);
			window.removeEventListener("resize", schedule);
			document.removeEventListener("selectionchange", schedule);
		};
	}, [range, viewportRef, buttonRef, onInvalid]);
	return position;
}
