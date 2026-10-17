import { useCallback, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * A long composer menu list: scrolls on an inner strip, with a cue at the bottom
 * while more rows sit below. A change in which rows are listed restarts it at the top.
 */
export function ModelMenuChoices({ items, children }: { items: readonly { id: string }[]; children: ReactNode }) {
	// Lists are often rebuilt each render; only a different set of rows restarts the strip.
	const signature = items.map((item) => item.id).join("\n");
	const scrollRef = useRef<HTMLDivElement>(null);
	const [canScrollDown, setCanScrollDown] = useState(false);
	const updateScrollCue = useCallback(() => {
		const element = scrollRef.current;
		setCanScrollDown(Boolean(element && element.scrollHeight - element.scrollTop > element.clientHeight + 1));
	}, []);
	useLayoutEffect(() => {
		if (scrollRef.current) scrollRef.current.scrollTop = 0;
		updateScrollCue();
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(updateScrollCue);
		observer.observe(element);
		return () => observer.disconnect();
	}, [signature, updateScrollCue]);

	return (
		<div className="flex min-h-0 max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] flex-col">
			<div className="relative grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] overflow-hidden">
				<div ref={scrollRef} className="model-menu-scroll min-h-0 overflow-y-auto overscroll-contain" onScroll={updateScrollCue}>
					{children}
				</div>
				<div
					className={cn("model-menu-overflow-cue", canScrollDown ? "opacity-100" : "opacity-0")}
					aria-hidden="true"
				/>
			</div>
		</div>
	);
}
