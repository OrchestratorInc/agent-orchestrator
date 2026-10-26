import { useCallback, useEffect, useRef, useState } from "react";

type ScrollBehavior = ScrollToOptions["behavior"];

// Tracks whether a horizontal tab strip has hidden content on either edge.
// The small tolerance avoids flickering from fractional layout measurements.
export function useTabScrollEdges(dependencies: readonly unknown[] = []) {
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const [edges, setEdges] = useState({ left: false, right: false });
	const [scrollingToEnd, setScrollingToEnd] = useState(false);
	const updateEdges = useCallback(() => {
		const element = scrollRef.current;
		if (!element) return;
		const overflow = element.scrollWidth - element.clientWidth;
		const next = {
			left: overflow > 1 && element.scrollLeft > 1,
			right: overflow > 1 && element.scrollLeft < overflow - 1,
		};
		setEdges((current) => (current.left === next.left && current.right === next.right ? current : next));
		if (!next.right) setScrollingToEnd(false);
	}, []);
	const scrollToEnd = useCallback((behavior: ScrollBehavior = "smooth") => {
		const element = scrollRef.current;
		if (!element) return;
		setScrollingToEnd(true);
		element.scrollTo({ left: element.scrollWidth, behavior });
		window.requestAnimationFrame(updateEdges);
	}, [updateEdges]);

	useEffect(() => {
		const element = scrollRef.current;
		if (!element) return;
		updateEdges();
		// Drive the strip from every wheel event over it, whichever axis
		// dominates. Left to the browser, a trackpad gesture that starts slightly
		// vertical latches onto an outer scroller and the strip stops responding.
		const handleWheel = (event: WheelEvent) => {
			if (event.ctrlKey || event.metaKey || element.scrollWidth <= element.clientWidth) return;
			const delta = Math.abs(event.deltaX) >= Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
			if (delta === 0) return;
			event.preventDefault();
			element.scrollBy({ left: delta });
		};
		element.addEventListener("scroll", updateEdges, { passive: true });
		element.addEventListener("wheel", handleWheel, { passive: false });
		const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateEdges);
		observer?.observe(element);
		return () => {
			element.removeEventListener("scroll", updateEdges);
			element.removeEventListener("wheel", handleWheel);
			observer?.disconnect();
		};
	}, [updateEdges, ...dependencies]);

	return {
		scrollRef,
		scrollToEnd,
		showLeftFade: edges.left,
		showRightFade: edges.right && !scrollingToEnd,
		updateEdges,
	};
}
