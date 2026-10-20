import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent } from "react";

// The first preview waits out a short hover and fades in. Once one is showing,
// sweeping across the other rows swaps previews instantly with no animation,
// until the pointer has been off every row for COOL_DOWN_MS.
const FIRST_OPEN_DELAY_MS = 350;
const COOL_DOWN_MS = 300;

// Shared by every row: a preview opened on one row warms all of them.
let warm = false;
let coolTimer: ReturnType<typeof setTimeout> | undefined;

function markShowing() {
	clearTimeout(coolTimer);
	warm = true;
}

function markHidden() {
	clearTimeout(coolTimer);
	coolTimer = setTimeout(() => {
		warm = false;
	}, COOL_DOWN_MS);
}

/** Test hook: start the next hover from cold. */
export function resetSessionPreviewWarmth() {
	clearTimeout(coolTimer);
	warm = false;
}

export type SessionPreviewAnimation = "enter" | "none";

/**
 * Open state for one row's hover preview. The row owns its pointer and focus
 * events through `triggerProps`, so leaving a row always cancels a pending
 * open: a controlled Radix card never reports a close for a card that is not
 * open yet, which would leave the delayed open running after the pointer left.
 */
export function useSessionPreviewHover(suppressed: boolean) {
	const [state, setState] = useState<{ open: boolean; animation: SessionPreviewAnimation }>({
		open: false,
		animation: "enter",
	});
	const openRef = useRef(false);
	const timerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

	const hide = useCallback(() => {
		clearTimeout(timerRef.current);
		if (!openRef.current) return;
		openRef.current = false;
		markHidden();
		setState((current) => ({ ...current, open: false }));
	}, []);

	const show = useCallback((animation: SessionPreviewAnimation) => {
		openRef.current = true;
		markShowing();
		setState({ open: true, animation });
	}, []);

	const request = useCallback(() => {
		clearTimeout(timerRef.current);
		if (openRef.current) return;
		if (warm) show("none");
		else timerRef.current = setTimeout(() => show("enter"), FIRST_OPEN_DELAY_MS);
	}, [show]);

	// A drag in progress closes the preview and keeps it from coming back.
	useEffect(() => {
		if (suppressed) hide();
	}, [hide, suppressed]);
	useEffect(() => () => hide(), [hide]);

	const triggerProps = useMemo(
		() => ({
			onBlur: hide,
			onFocus: request,
			onPointerEnter: (event: PointerEvent) => {
				if (event.pointerType !== "touch") request();
			},
			onPointerLeave: hide,
		}),
		[hide, request],
	);

	return { open: state.open && !suppressed, animation: state.animation, triggerProps };
}
