import { memo, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/utils";
import { PAIRING_QR_VERSION, QrCanvas } from "./QrCanvas";

export const QR_FADE_MS = 200;

const QR_RETIRE_SLACK_MS = 50;

function prefersReducedMotion(): boolean {
	return typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches === true;
}

interface Slot {
	value: string | null;
	visible: boolean;
	fade: boolean;
}

interface Model {
	slots: [Slot, Slot];
	front: 0 | 1;
}

const EMPTY: Slot = { value: null, visible: false, fade: false };
const EMPTY_MODEL: Model = { slots: [EMPTY, EMPTY], front: 0 };

function modelWith(front: 0 | 1, slot: Slot): Model {
	return { slots: front === 0 ? [slot, EMPTY] : [EMPTY, slot], front };
}

export const PairingQr = memo(function PairingQr({
	value,
	size,
	animate = false,
	className,
}: {
	value: string | null;
	size: number;
	animate?: boolean;
	className?: string;
}) {
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
			const other = settled.slots[1 - settled.front];
			if (other.value !== null || !current.visible || current.fade) setModel(base);
			return cancel;
		}

		const next = (current.value ? 1 - settled.front : settled.front) as 0 | 1;
		if (instant) {
			setModel(modelWith(next, { value, visible: true, fade: false }));
			return cancel;
		}
		const slots: [Slot, Slot] = [EMPTY, EMPTY];
		slots[settled.front] = base.slots[settled.front];
		slots[next] = { value, visible: false, fade: false };
		setModel({ slots, front: next });
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
