import { useCallback, useEffect, useRef, useState } from "react";

export function useOverflowScroll<T extends HTMLElement>(watch: unknown) {
	const ref = useRef<T | null>(null);
	const [canScrollLeft, setCanScrollLeft] = useState(false);
	const [canScrollRight, setCanScrollRight] = useState(false);

	useEffect(() => {
		const el = ref.current;
		if (!el) return;
		const update = () => {
			setCanScrollLeft(el.scrollLeft > 0);
			setCanScrollRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
		};
		update();
		el.addEventListener("scroll", update, { passive: true });
		const onWheel = (event: WheelEvent) => {
			if (event.ctrlKey || event.metaKey) return;
			const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
			if (delta === 0 || el.scrollWidth <= el.clientWidth) return;
			event.preventDefault();
			el.scrollBy({ left: delta });
		};
		el.addEventListener("wheel", onWheel, { passive: false });
		const observer = new ResizeObserver(update);
		observer.observe(el);
		for (const child of Array.from(el.children)) observer.observe(child);
		return () => {
			el.removeEventListener("scroll", update);
			el.removeEventListener("wheel", onWheel);
			observer.disconnect();
		};
	}, [watch]);

	const scrollByDirection = useCallback((direction: -1 | 1) => {
		const el = ref.current;
		if (!el) return;
		el.scrollBy({ left: direction * el.clientWidth * 0.8, behavior: "smooth" });
	}, []);

	return { ref, canScrollLeft, canScrollRight, scrollByDirection };
}
