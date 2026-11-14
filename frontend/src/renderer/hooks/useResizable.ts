import { useCallback, useEffect, useLayoutEffect, useRef } from "react";

type ResizableConstraint = number | (() => number);

interface UseResizableOptions {
	cssVar: string;
	getCssTargets?: () => ReadonlyArray<HTMLElement | null>;
	storageKey: string;
	defaultWidth: number;
	min: ResizableConstraint;
	max: ResizableConstraint;
	edge: "left" | "right";
	onExpand?: () => void;
	restoreMin?: number;
	reclampOnWindowResize?: boolean;
	expandDragThreshold?: number;
}

export function useResizable({
	cssVar,
	getCssTargets,
	storageKey,
	defaultWidth,
	min,
	max,
	edge,
	onExpand,
	restoreMin,
	reclampOnWindowResize = false,
	expandDragThreshold = 8,
}: UseResizableOptions) {
	const widthRef = useRef(defaultWidth);
	const preferredWidthRef = useRef(defaultWidth);
	const frameRef = useRef<number | null>(null);
	const pendingWidthRef = useRef<number | null>(null);
	const appliedTargetsRef = useRef(new Set<HTMLElement>());
	const activeDragCleanupRef = useRef<(() => void) | null>(null);
	const minValue = useCallback(() => (typeof min === "function" ? min() : min), [min]);
	const maxValue = useCallback(() => (typeof max === "function" ? max() : max), [max]);
	const cssTargets = useCallback(
		() =>
			getCssTargets
				? getCssTargets().filter((target): target is HTMLElement => target !== null)
				: [document.documentElement],
		[getCssTargets],
	);

	const apply = useCallback(
		(next: number) => {
			const clamped = Math.min(maxValue(), Math.max(minValue(), next));
			widthRef.current = clamped;
			const targets = cssTargets();
			for (const target of targets) {
				if (target.style.getPropertyValue(cssVar) !== `${clamped}px`) {
					target.style.setProperty(cssVar, `${clamped}px`);
			}
				appliedTargetsRef.current.add(target);
			}
		},
		[cssTargets, cssVar, maxValue, minValue],
	);

	const applyOnFrame = useCallback(
		(next: number) => {
			pendingWidthRef.current = next;
			if (frameRef.current !== null) return;
			frameRef.current = window.requestAnimationFrame(() => {
				frameRef.current = null;
				const pending = pendingWidthRef.current;
				pendingWidthRef.current = null;
				if (pending !== null) apply(pending);
			});
		},
		[apply],
	);

	const flushPending = useCallback(() => {
		if (frameRef.current !== null) {
			window.cancelAnimationFrame(frameRef.current);
			frameRef.current = null;
		}
		const pending = pendingWidthRef.current;
		pendingWidthRef.current = null;
		if (pending !== null) apply(pending);
	}, [apply]);

	useLayoutEffect(() => {
		const saved = Number(window.localStorage.getItem(storageKey));
		const restored = Number.isFinite(saved) && saved > 0 ? saved : defaultWidth;
		preferredWidthRef.current = restoreMin === undefined ? restored : Math.max(restoreMin, restored);
		apply(preferredWidthRef.current);
		return () => {
			activeDragCleanupRef.current?.();
			if (frameRef.current !== null) window.cancelAnimationFrame(frameRef.current);
			for (const target of appliedTargetsRef.current) target.style.removeProperty(cssVar);
			appliedTargetsRef.current.clear();
		};
	}, [apply, cssVar, defaultWidth, restoreMin, storageKey]);

	useEffect(() => {
		if (!reclampOnWindowResize) return;
		let frame: number | null = null;
		const onResize = () => {
			if (frame !== null) return;
			frame = window.requestAnimationFrame(() => {
				frame = null;
				if (activeDragCleanupRef.current) return;
				const next = Math.min(maxValue(), Math.max(minValue(), preferredWidthRef.current));
				if (next > widthRef.current + 0.5) apply(next);
			});
		};
		window.addEventListener("resize", onResize);
		return () => {
			window.removeEventListener("resize", onResize);
			if (frame !== null) window.cancelAnimationFrame(frame);
		};
	}, [apply, maxValue, minValue, reclampOnWindowResize]);

	const onPointerDown = useCallback(
		(event: React.PointerEvent<HTMLElement>) => {
			activeDragCleanupRef.current?.();
			event.preventDefault();
			const pointerId = event.pointerId;
			const captureTarget = event.currentTarget;
			captureTarget.setPointerCapture?.(pointerId);
			const startX = event.clientX;
			const visualWidth = cssTargets()
				.map((target) => target.getBoundingClientRect().width)
				.find((width) => width > 0);
			const seedWidth =
				visualWidth !== undefined && Math.abs(visualWidth - widthRef.current) > 0.5 ? visualWidth : widthRef.current;
			const startWidth = Math.min(maxValue(), Math.max(minValue(), seedWidth));
			const sign = edge === "right" ? 1 : -1;
			let changed = false;
			document.body.classList.add("is-resizing-x");

			const finish = () => {
				window.removeEventListener("pointermove", onMove);
				window.removeEventListener("pointerup", onEnd);
				window.removeEventListener("pointercancel", onEnd);
				window.removeEventListener("blur", finish);
				flushPending();
				document.body.classList.remove("is-resizing-x");
				if (captureTarget.hasPointerCapture?.(pointerId)) captureTarget.releasePointerCapture(pointerId);
				if (changed) {
					preferredWidthRef.current = widthRef.current;
					window.localStorage.setItem(storageKey, String(widthRef.current));
				}
				if (activeDragCleanupRef.current === finish) activeDragCleanupRef.current = null;
			};
			const onEnd = (e: PointerEvent) => {
				if (e.pointerId === pointerId) finish();
			};
			const onMove = (e: PointerEvent) => {
				if (e.pointerId !== pointerId) return;
				const next = Math.min(maxValue(), Math.max(minValue(), startWidth + sign * (e.clientX - startX)));
				if (!changed && Math.abs(next - startWidth) < 0.5) return;
				changed = true;
				applyOnFrame(next);
			};
			window.addEventListener("pointermove", onMove);
			window.addEventListener("pointerup", onEnd);
			window.addEventListener("pointercancel", onEnd);
			window.addEventListener("blur", finish);
			activeDragCleanupRef.current = finish;
		},
		[apply, applyOnFrame, cssTargets, edge, flushPending, maxValue, minValue, storageKey],
	);

	const onCollapsedPointerDown = useCallback(
		(event: React.PointerEvent<HTMLElement>) => {
			activeDragCleanupRef.current?.();
			const pointerId = event.pointerId;
			const captureTarget = event.currentTarget;
			captureTarget.setPointerCapture?.(pointerId);
			const startX = event.clientX;
			const sign = edge === "right" ? 1 : -1;
			let expanded = false;
			document.body.classList.add("is-resizing-x");

			const finish = () => {
				window.removeEventListener("pointermove", onMove);
				window.removeEventListener("pointerup", onEnd);
				window.removeEventListener("pointercancel", onEnd);
				window.removeEventListener("blur", finish);
				flushPending();
				document.body.classList.remove("is-resizing-x");
				if (captureTarget.hasPointerCapture?.(pointerId)) captureTarget.releasePointerCapture(pointerId);
				if (expanded) {
					preferredWidthRef.current = widthRef.current;
					window.localStorage.setItem(storageKey, String(widthRef.current));
				}
				if (activeDragCleanupRef.current === finish) activeDragCleanupRef.current = null;
			};
			const onEnd = (e: PointerEvent) => {
				if (e.pointerId === pointerId) finish();
			};
			const onMove = (e: PointerEvent) => {
				if (e.pointerId !== pointerId) return;
				const delta = sign * (e.clientX - startX);
				if (delta < expandDragThreshold) return;
				if (!expanded) {
					expanded = true;
					onExpand?.();
				}
				applyOnFrame(minValue() + delta);
			};
			window.addEventListener("pointermove", onMove);
			window.addEventListener("pointerup", onEnd);
			window.addEventListener("pointercancel", onEnd);
			window.addEventListener("blur", finish);
			activeDragCleanupRef.current = finish;
		},
		[applyOnFrame, edge, expandDragThreshold, flushPending, minValue, onExpand, storageKey],
	);

	const onDoubleClick = useCallback(() => {
		preferredWidthRef.current = defaultWidth;
		apply(defaultWidth);
		window.localStorage.setItem(storageKey, String(defaultWidth));
	}, [apply, defaultWidth, storageKey]);

	return { onPointerDown, onCollapsedPointerDown, onDoubleClick };
}
