/**
 * Swaps one inline label for another without a jump. When `swapKey` changes the
 * old content folds into a small round shape and fades, then the new content
 * grows out of that shape to its full width (a pill expands from its icon, text
 * wipes in), while the container's width eases on the same curve. Content that
 * changes under the same key only eases the width. Reduced motion swaps
 * instantly (styles.css).
 */

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/utils";

/** Long enough for the leave animation in styles.css to finish. */
const LEAVE_MS = 200;

type Layer = { key: string; node: ReactNode };

export function SmoothSwap({ swapKey, children, className, appear = false }: {
	swapKey: string;
	children: ReactNode;
	className?: string;
	/** Grow in from nothing when first mounted, rather than starting in place. */
	appear?: boolean;
}) {
	const [shownKey, setShownKey] = useState(swapKey);
	const [leaving, setLeaving] = useState<Layer | null>(null);
	const [swapped, setSwapped] = useState(appear);
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
	const [width, setWidth] = useState<number | undefined>(appear ? 0 : undefined);
	useLayoutEffect(() => {
		const element = current.current;
		if (!element) return;
		// Nothing laid out (a hidden tab, a test DOM) has no offsetParent: leave the width natural.
		// An empty layer that is laid out measures 0 on purpose, so the width can ease to and from it.
		const measure = () => setWidth(element.offsetParent ? element.offsetWidth : undefined);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [swapKey]);

	return (
		<span data-smooth-swap="" className={cn("smooth-swap", className)} style={width === undefined ? undefined : { width }}>
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
