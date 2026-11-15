import { useCallback, useEffect, useMemo, useRef, useState, type RefCallback } from "react";
import { syncEdgeFade } from "./useEdgeFade";

type ScrollBehavior = ScrollToOptions["behavior"];

export function useTabScrollEdges(dependencies: readonly unknown[] = []) {
	const [element, setElement] = useState<HTMLDivElement | null>(null);
	const scrollRef = useMemo(() => {
		const ref = ((node: HTMLDivElement | null) => {
			ref.current = node;
			setElement(node);
		}) as RefCallback<HTMLDivElement> & { current: HTMLDivElement | null };
		ref.current = null;
		return ref;
	}, []);
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
		const handleWheel = (event: WheelEvent) => {
			if (event.ctrlKey || event.metaKey || element.scrollWidth <= element.clientWidth) return;
			const delta = Math.abs(event.deltaX) >= Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
			if (delta === 0) return;
			event.preventDefault();
			scrollingToEnd.current = false;
			element.scrollBy({ left: delta });
		};
		const handleScrollEnd = () => {
			scrollingToEnd.current = false;
			updateEdges();
		};
		element.addEventListener("scroll", updateEdges, { passive: true });
		element.addEventListener("scrollend", handleScrollEnd, { passive: true });
		element.addEventListener("wheel", handleWheel, { passive: false });
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
