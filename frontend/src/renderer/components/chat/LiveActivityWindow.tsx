/**
 * A live turn's work, in a window that follows it.
 *
 * While the agent works, its tool calls and interim notes arrive faster than anyone
 * reads them, and left to grow they push everything else down the screen. Here they
 * scroll inside a fixed-height window that keeps the newest step in view, with older
 * steps fading out at the top edge. Scrolling up, clicking inside, or moving focus or
 * pressing a key inside takes over until the reader is back at the bottom: by scrolling
 * there, or by leaving the window while already there. When the turn finishes the
 * window gives way to "Worked for", which keeps the whole record.
 */

import { useCallback, useLayoutEffect, useRef, type ReactNode } from "react";
import { useScrollFollow } from "../../hooks/useScrollFollow";
import { syncEdgeFade } from "./edge-fade";

export function LiveActivityWindow({ children }: { children: ReactNode }) {
	const viewportRef = useRef<HTMLDivElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const following = useRef(true);
	const getNode = useCallback(() => viewportRef.current, []);
	const { followEnd, isOwnScroll, cancel } = useScrollFollow(getNode);

	useLayoutEffect(() => {
		const viewport = viewportRef.current;
		const content = contentRef.current;
		if (!viewport || !content) return;
		// Land on the newest step without gliding there from the top.
		viewport.scrollTop = viewport.scrollHeight;
		syncEdgeFade(viewport);
		const observer = new ResizeObserver(() => {
			if (following.current) followEnd();
			syncEdgeFade(viewport);
		});
		observer.observe(content);
		observer.observe(viewport);
		return () => observer.disconnect();
	}, [followEnd]);

	const onScroll = () => {
		const viewport = viewportRef.current;
		if (!viewport) return;
		syncEdgeFade(viewport);
		if (isOwnScroll(viewport.scrollTop)) return;
		// The reader's scroll decides: back at the bottom resumes following.
		following.current = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= 2;
	};
	// Reading or opening something in the window: stop pulling it to the bottom.
	const takeOver = () => {
		following.current = false;
		cancel();
	};
	// Done reading: the pointer or focus has left. A click that moved nothing fires no
	// scroll event, so a reader already at the bottom would otherwise stay stranded.
	const resumeIfAtEnd = () => {
		const viewport = viewportRef.current;
		if (!viewport) return;
		if (viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= 2) following.current = true;
	};

	return (
		<div
			ref={viewportRef}
			onScroll={onScroll}
			onWheel={(event) => {
				if (event.deltaY < 0) takeOver();
			}}
			onPointerDown={takeOver}
			// The keyboard path to the same takeover: tabbing onto a step, or a key
			// that opens or scrolls one, means the reader is working inside the window.
			onFocus={takeOver}
			onKeyDown={takeOver}
			onMouseLeave={resumeIfAtEnd}
			onBlur={(event) => {
				if (!event.currentTarget.contains(event.relatedTarget)) resumeIfAtEnd();
			}}
			data-live-activity=""
			className="edge-fade-viewport scrollbar-none max-h-60 min-w-0 overflow-y-auto"
		>
			<div ref={contentRef} className="flex min-w-0 flex-col gap-2.5">
				{children}
			</div>
		</div>
	);
}
