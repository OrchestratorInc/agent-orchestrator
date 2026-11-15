import { useCallback, useEffect, useMemo, useRef, useState, type RefCallback } from "react";
import { syncEdgeFade } from "./useEdgeFade";

type ScrollBehavior = ScrollToOptions["behavior"];

// Fades a horizontal tab strip's edges while tabs sit past them (the strip
// needs `edge-fade edge-fade-x`), and drives it from the wheel. The fade is
// set as data attributes, so scrolling never re-renders the owner.
export function useTabScrollEdges(dependencies: readonly unknown[] = []) {
	// A callback ref, so the listeners below attach whenever the strip mounts,
	// even after its owner rendered (a portal waiting for its host). `current`
	// keeps owners' object-ref reads working.
	const [element, setElement] = useState<HTMLDivElement | null>(null);
	const scrollRef = useMemo(() => {
		const ref = ((node: HTMLDivElement | null) => {
			ref.current = node;
			setElement(node);
		}) as RefCallback<HTMLDivElement> & { current: HTMLDivElement | null };
		ref.current = null;
		return ref;
	}, []);
	// Gliding to the end (a new tab) holds the end fade off, so the tab being
	// revealed is not faded on its way in.
	const scrollingToEnd = useRef(false);
	const attachedElement = useRef<HTMLDivElement | null>(null);
	const updateEdges = useCallback(() => {
		const element = scrollRef.current;
		if (!element) return;
		if (element.scrollLeft >= element.scrollWidth - element.clientWidth - 1) scrollingToEnd.current = false;
		syncEdgeFade(element, "x", scrollingToEnd.current);
	}, [scrollRef]);
	const scrollToEnd = useCallback((behavior: ScrollBehavior = "smooth") => {
		const element = scrollRef.current;
		if (!element) return;
		scrollingToEnd.current = true;
		element.scrollTo({ left: element.scrollWidth, behavior });
		window.requestAnimationFrame(updateEdges);
	}, [scrollRef, updateEdges]);

	useEffect(() => {
		if (!element) return;
		// Where a strip sits as it mounts is not a change: its first fades apply
		// at once, and only later ones ease (see `data-fade-instant`).
		let settleFrame: number | null = null;
		if (attachedElement.current !== element) {
			attachedElement.current = element;
			element.setAttribute("data-fade-instant", "");
			settleFrame = window.requestAnimationFrame(() => {
				settleFrame = window.requestAnimationFrame(() => {
					settleFrame = null;
					element.removeAttribute("data-fade-instant");
				});
			});
		}
		updateEdges();
		// Drive the strip from every wheel event over it, whichever axis
		// dominates. Left to the browser, a trackpad gesture that starts slightly
		// vertical latches onto an outer scroller and the strip stops responding.
		const handleWheel = (event: WheelEvent) => {
			if (event.ctrlKey || event.metaKey || element.scrollWidth <= element.clientWidth) return;
			const delta = Math.abs(event.deltaX) >= Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
			if (delta === 0) return;
			event.preventDefault();
			scrollingToEnd.current = false;
			element.scrollBy({ left: delta });
		};
		// A glide that stops short (the reader took over, the strip resized)
		// releases the end fade it was holding off.
		const handleScrollEnd = () => {
			scrollingToEnd.current = false;
			updateEdges();
		};
		element.addEventListener("scroll", updateEdges, { passive: true });
		element.addEventListener("scrollend", handleScrollEnd, { passive: true });
		element.addEventListener("wheel", handleWheel, { passive: false });
		// Read the strip's overflow in the next frame, not inside the observer
		// pass. Another observer (a terminal refit, a chat reflow) has usually
		// just changed layout there, so reading scrollWidth forced a full
		// synchronous layout on every frame of the inspector slide.
		let frame: number | null = null;
		const scheduleUpdate = () => {
			if (frame !== null) return;
			frame = window.requestAnimationFrame(() => {
				frame = null;
				updateEdges();
			});
		};
		const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(scheduleUpdate);
		observer?.observe(element);
		return () => {
			element.removeEventListener("scroll", updateEdges);
			element.removeEventListener("scrollend", handleScrollEnd);
			element.removeEventListener("wheel", handleWheel);
			observer?.disconnect();
			if (frame !== null) window.cancelAnimationFrame(frame);
			if (settleFrame !== null) {
				window.cancelAnimationFrame(settleFrame);
				element.removeAttribute("data-fade-instant");
			}
		};
	}, [element, updateEdges, ...dependencies]);

	return {
		scrollRef,
		scrollToEnd,
		updateEdges,
	};
}
