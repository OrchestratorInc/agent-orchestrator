import { memo, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/utils";
import { PAIRING_QR_VERSION, QrCanvas } from "./QrCanvas";

/** Crossfade between codes: long enough to read as one code becoming the
 *  next, short enough that a fresh code is scannable at once. Matches the
 *  `duration-200` on each slot below. */
export const QR_FADE_MS = 200;

/** Slack after a fade before the code beneath it is dropped, so it never goes
 *  while the new one is still partly transparent. */
const QR_RETIRE_SLACK_MS = 50;

function prefersReducedMotion(): boolean {
	return typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches === true;
}

/** One of the two canvases. `fade` animates the next change of `visible`. */
interface Slot {
	value: string | null;
	visible: boolean;
	fade: boolean;
}

interface Model {
	slots: [Slot, Slot];
	/** The slot holding the newest code: it draws above the other. */
	front: 0 | 1;
}

const EMPTY: Slot = { value: null, visible: false, fade: false };
const EMPTY_MODEL: Model = { slots: [EMPTY, EMPTY], front: 0 };

function modelWith(front: 0 | 1, slot: Slot): Model {
	return { slots: front === 0 ? [slot, EMPTY] : [EMPTY, slot], front };
}

/**
 * The pairing code, crossfaded whenever it changes.
 *
 * Two canvases, one per code. A new code is drawn hidden into the free canvas
 * (drawing is synchronous), then faded in over the current one, which stays
 * fully opaque beneath until the fade has finished and is only then cleared:
 * no blank card between the two, and the placeholder never shows through a
 * half-faded code. Only the opacity animates, so a change costs the compositor
 * one layer fade and no repaint.
 *
 * `value` null fades the code out, revealing whatever the caller has placed
 * behind this component (the blurred placeholder), and clears the canvases.
 *
 * Fading is opt-in through `animate`, which the caller sets only once the user
 * has generated or regenerated a code. A code that already exists when the page
 * opens is on screen in the first frame, with no fade or placeholder, and a
 * change nobody asked for swaps at once. Reduced motion makes every change such
 * a swap.
 *
 * Fills its parent, which owns the fixed square size, so a code change can
 * never shift the layout.
 */
export const PairingQr = memo(function PairingQr({
	value,
	size,
	animate = false,
	className,
}: {
	/** The real pairing code, or null when there is none to show. */
	value: string | null;
	size: number;
	/** Crossfade code changes from now on. Off at mount, so opening the page
	 *  never plays a transition. */
	animate?: boolean;
	className?: string;
}) {
	// A code present at mount is shown at once: it is not a change.
	const [model, setModel] = useState<Model>(() => (value ? modelWith(0, { value, visible: true, fade: false }) : EMPTY_MODEL));
	const modelRef = useRef(model);
	modelRef.current = model;
	const animateRef = useRef(animate);
	animateRef.current = animate;
	const pending = useRef<{ frame?: number; timer?: ReturnType<typeof setTimeout> }>({});

	useEffect(() => {
		const cancel = () => {
			if (pending.current.frame !== undefined) cancelAnimationFrame(pending.current.frame);
			if (pending.current.timer !== undefined) clearTimeout(pending.current.timer);
			pending.current = {};
		};
		cancel();

		// Settle whatever an earlier change left in flight: the newest code is
		// fully shown, the one beneath it gone.
		const settled = modelRef.current;
		const current = settled.slots[settled.front];
		const base: Model = current.value ? modelWith(settled.front, { value: current.value, visible: true, fade: false }) : EMPTY_MODEL;
		const instant = !animateRef.current || prefersReducedMotion();

		if (value === null) {
			if (!current.value) return cancel;
			if (instant) {
				setModel(EMPTY_MODEL);
				return cancel;
			}
			setModel({ ...base, slots: base.slots.map((slot) => (slot.value ? { ...slot, visible: false, fade: true } : slot)) as [Slot, Slot] });
			pending.current.timer = setTimeout(() => setModel(EMPTY_MODEL), QR_FADE_MS + QR_RETIRE_SLACK_MS);
			return cancel;
		}

		if (current.value === value) {
			// Already the newest code: only an unfinished change needs settling.
			const other = settled.slots[1 - settled.front];
			if (other.value !== null || !current.visible || current.fade) setModel(base);
			return cancel;
		}

		const next = (current.value ? 1 - settled.front : settled.front) as 0 | 1;
		if (instant) {
			setModel(modelWith(next, { value, visible: true, fade: false }));
			return cancel;
		}
		// Drawn hidden above the current code, which stays where it is.
		const slots: [Slot, Slot] = [EMPTY, EMPTY];
		slots[settled.front] = base.slots[settled.front];
		slots[next] = { value, visible: false, fade: false };
		setModel({ slots, front: next });
		// Two frames, so the hidden state is painted before the fade starts;
		// flipping it in the frame the canvas mounted in would skip the fade.
		pending.current.frame = requestAnimationFrame(() => {
			pending.current.frame = requestAnimationFrame(() => {
				setModel((prev) => ({
					...prev,
					slots: prev.slots.map((slot, index) => (index === next && slot.value === value ? { ...slot, visible: true, fade: true } : slot)) as [Slot, Slot],
				}));
				pending.current.timer = setTimeout(
					() => setModel((prev) => ({ ...prev, slots: prev.slots.map((slot, index) => (index === next ? { ...slot, fade: false } : EMPTY)) as [Slot, Slot] })),
					QR_FADE_MS + QR_RETIRE_SLACK_MS,
				);
			});
		});
		return cancel;
	}, [value]);

	return (
		<div className={cn("absolute inset-0", className)}>
			{model.slots.map((slot, index) =>
				slot.value === null ? null : (
					<div
						key={index}
						aria-hidden={slot.value !== value}
						className={cn(
							// Opaque, so a canvas that has finished fading in hides the one
							// beneath it completely.
							"absolute inset-0 bg-white",
							index === model.front ? "z-10" : "z-0",
							slot.fade ? "transition-opacity duration-200 ease-out motion-reduce:transition-none" : "transition-none",
							slot.visible ? "opacity-100" : "opacity-0",
						)}
					>
						<QrCanvas
							value={slot.value}
							data-qr-value={slot.value === value ? slot.value : undefined}
							size={size}
							version={PAIRING_QR_VERSION}
							className="block"
						/>
					</div>
				),
			)}
		</div>
	);
});
