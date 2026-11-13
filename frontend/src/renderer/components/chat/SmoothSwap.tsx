/**
 * Swaps one inline label for another without a jump.
 *
 * When `swapKey` changes, the outgoing content fades up and out in place while
 * the new content fades in from just below, and the container's width eases
 * from one to the other, so whatever sits beside it slides rather than snaps.
 * Content that changes under the same key (a model loading, a new name) only
 * eases the width. Reduced motion swaps instantly.
 */

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/utils";

/** Long enough for the leave animation in styles.css to finish. */
const LEAVE_MS = 220;

type Layer = { key: string; node: ReactNode };

export function SmoothSwap({ swapKey, children, className }: { swapKey: string; children: ReactNode; className?: string }) {
	const [shownKey, setShownKey] = useState(swapKey);
	const [leaving, setLeaving] = useState<Layer | null>(null);
	const [swapped, setSwapped] = useState(false);
	const lastChildren = useRef(children);

	// Derived during render so the outgoing layer and the new one land in the same paint.
	if (shownKey !== swapKey) {
		setShownKey(swapKey);
		setLeaving({ key: shownKey, node: lastChildren.current });
		setSwapped(true);
	}
	useEffect(() => {
		lastChildren.current = children;
	});
	useEffect(() => {
		if (!leaving) return;
		const timer = window.setTimeout(() => setLeaving(null), LEAVE_MS);
		return () => window.clearTimeout(timer);
	}, [leaving]);

	const current = useRef<HTMLSpanElement>(null);
	const [width, setWidth] = useState<number>();
	// While the width eases, its trailing edge fades instead of cutting content off.
	const [moving, setMoving] = useState(false);
	const lastWidth = useRef<number | undefined>(undefined);
	useEffect(() => {
		if (width !== undefined && lastWidth.current !== undefined && width !== lastWidth.current) setMoving(true);
		lastWidth.current = width;
	}, [width]);
	useLayoutEffect(() => {
		const element = current.current;
		if (!element) return;
		// Zero means nothing was laid out (a hidden tab, a test DOM): leave the width natural.
		const measure = () => setWidth(element.offsetWidth || undefined);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [swapKey]);

	return (
		<span
			data-smooth-swap=""
			data-moving={moving ? "" : undefined}
			className={cn("smooth-swap", className)}
			style={width ? { width } : undefined}
			onTransitionEnd={(event) => { if (event.propertyName === "width") setMoving(false); }}
		>
			{leaving ? (
				<span key={`leave:${leaving.key}`} aria-hidden="true" className="smooth-swap-layer" data-leave="">
					{leaving.node}
				</span>
			) : null}
			<span key={swapKey} ref={current} className="smooth-swap-layer" data-enter={swapped ? "" : undefined}>
				{children}
			</span>
		</span>
	);
}
