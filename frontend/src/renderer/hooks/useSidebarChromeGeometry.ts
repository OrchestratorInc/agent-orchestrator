import { useCallback, useLayoutEffect, useRef, type RefCallback, type RefObject } from "react";
import { isMacPlatform } from "../lib/platform";

/** `rail`: 0 open to 1 at the rail, set only for a sidebar that collapses to one. */
export type SidebarChromeGeometry = { width: number; progress: number; rail?: number };

const WIDTH_VAR = "--ao-sidebar-layout-width";
const PROGRESS_VAR = "--ao-sidebar-collapse-progress";
const RAIL_VAR = "--ao-sidebar-rail-progress";

// The sidebar animates its layout width every frame. Publishing that on <html>
// restyled the whole document each frame, so the value lives in this tiny store
// and only the session headers that consume it write it onto themselves.
let current: SidebarChromeGeometry | null = null;
const listeners = new Set<(geometry: SidebarChromeGeometry | null) => void>();

export function getSidebarChromeGeometry(): SidebarChromeGeometry | null {
	return current;
}

export function setSidebarChromeGeometry(next: SidebarChromeGeometry | null): void {
	if (next === current) return;
	if (next && current && next.width === current.width && next.progress === current.progress && next.rail === current.rail) return;
	current = next;
	for (const listener of [...listeners]) listener(next);
}

export function subscribeSidebarChromeGeometry(listener: (geometry: SidebarChromeGeometry | null) => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

function collapseProgress(width: number, expandedWidth: number): number {
	return expandedWidth > 0 ? Math.max(0, Math.min(1, 1 - width / expandedWidth)) : 1;
}

/** Share the sidebar's actual animated layout with the fixed native chrome. */
/**
 * `rail`: the collapsed sidebar keeps a rail, so the gap's width alone says how
 * far the panel's chrome must clear the native buttons; progress stays 0.
 */
export function useSidebarChromeGeometry(ready: boolean, gapRef: RefObject<HTMLDivElement | null>, containerRef: RefObject<HTMLDivElement | null>, rail = false): void {
	useLayoutEffect(() => {
		if (!ready || !isMacPlatform()) return;
		const gap = gapRef.current;
		const container = containerRef.current;
		if (!gap || !container) return;
		// One synchronous read so the first paint is already correct. Every later
		// update comes from ResizeObserver entry sizes, which carry no forced layout.
		let width = gap.getBoundingClientRect().width;
		let expandedWidth = container.getBoundingClientRect().width;
		// The rail's width is a registered <length>, so this reads as pixels. Turning
		// Interface v2 on at runtime can run this before the shell sets the root
		// attribute that defines it, so keep reading until it is there.
		let railWidth = 0;
		const railProgress = () => {
			if (!railWidth) railWidth = Number.parseFloat(getComputedStyle(gap).getPropertyValue("--sidebar-width-rail")) || 0;
			return expandedWidth > railWidth ? Math.max(0, Math.min(1, (expandedWidth - width) / (expandedWidth - railWidth))) : 1;
		};
		const publish = () =>
			setSidebarChromeGeometry(rail ? { width, progress: 0, rail: railProgress() } : { width, progress: collapseProgress(width, expandedWidth) });
		publish();
		const observer = new ResizeObserver((entries) => {
			for (const entry of entries) {
				const size = entry.borderBoxSize?.[0]?.inlineSize ?? entry.contentRect.width;
				if (entry.target === gap) width = size;
				else if (entry.target === container) expandedWidth = size;
			}
			publish();
		});
		observer.observe(gap);
		observer.observe(container);
		return () => {
			observer.disconnect();
			setSidebarChromeGeometry(null);
		};
	}, [ready, gapRef, containerRef, rail]);
}

/**
 * Callback ref for a session header that clears the native macOS chrome. It
 * writes the sidebar geometry variables onto that one element (no React state,
 * no re-render per frame) and drops them when the header unmounts. Pass
 * `enabled: false` where the header does not use the clearance class.
 */
export function useSidebarChromeClearanceRef<T extends HTMLElement>(enabled = true): RefCallback<T> {
	const cleanup = useRef<(() => void) | null>(null);
	return useCallback(
		(element: T | null) => {
			cleanup.current?.();
			cleanup.current = null;
			if (!element || !enabled) return;
			let railSet = false;
			const apply = (geometry: SidebarChromeGeometry | null) => {
				if (geometry) {
					element.style.setProperty(WIDTH_VAR, `${geometry.width}px`);
					element.style.setProperty(PROGRESS_VAR, String(geometry.progress));
					// Only a rail sidebar (Interface v2) publishes this; otherwise it is
					// never touched, so the per-frame writes stay as they were.
					if (geometry.rail !== undefined) {
						element.style.setProperty(RAIL_VAR, String(geometry.rail));
						railSet = true;
					} else if (railSet) {
						element.style.removeProperty(RAIL_VAR);
						railSet = false;
					}
				} else {
					element.style.removeProperty(WIDTH_VAR);
					element.style.removeProperty(PROGRESS_VAR);
					if (railSet) element.style.removeProperty(RAIL_VAR);
					railSet = false;
				}
			};
			apply(current);
			const unsubscribe = subscribeSidebarChromeGeometry(apply);
			cleanup.current = () => {
				unsubscribe();
				apply(null);
			};
		},
		[enabled],
	);
}
