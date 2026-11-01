import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent } from "react";

// The first preview waits out a short hover and fades in. Once one is showing,
// sweeping across the other rows swaps previews instantly with no animation,
// until the pointer has been off every row for COOL_DOWN_MS.
const FIRST_OPEN_DELAY_MS = 350;
const COOL_DOWN_MS = 300;
// Time to cross the gap from the row to its card (or back) without closing it.
const LEAVE_GRACE_MS = 120;

// Shared by every row: a preview opened on one row warms all of them.
let warm = false;
let coolTimer: ReturnType<typeof setTimeout> | undefined;
// The row whose preview is showing, so opening another row's preview replaces
// it at once instead of waiting out the leave grace.
let activeHide: (() => void) | undefined;

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
	activeHide = undefined;
}

export type SessionPreviewAnimation = "enter" | "none";

/**
 * Open state for one row's hover preview. The row owns its pointer and focus
 * events through `triggerProps`, so leaving a row always cancels a pending
 * open: a controlled Radix card never reports a close for a card that is not
 * open yet, which would leave the delayed open running after the pointer left.
 *
 * The card itself is hoverable (its PR links are clickable), so a showing
 * preview survives a short LEAVE_GRACE_MS off the row and stays open while the
 * pointer is on the card. Spread `contentProps` on the card for that.
 */
export function useSessionPreviewHover(suppressed: boolean) {
	const [state, setState] = useState<{ open: boolean; animation: SessionPreviewAnimation }>({
		open: false,
		animation: "enter",
	});
	const openRef = useRef(false);
	const openTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const leaveTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

	const hide = useCallback(() => {
		clearTimeout(openTimerRef.current);
		clearTimeout(leaveTimerRef.current);
		if (activeHide === hide) activeHide = undefined;
		if (!openRef.current) return;
		openRef.current = false;
		markHidden();
		setState((current) => ({ ...current, open: false }));
	}, []);

	const show = useCallback(
		(animation: SessionPreviewAnimation) => {
			if (activeHide && activeHide !== hide) activeHide();
			activeHide = hide;
			openRef.current = true;
			markShowing();
			setState({ open: true, animation });
		},
		[hide],
	);

	const request = useCallback(() => {
		clearTimeout(openTimerRef.current);
		clearTimeout(leaveTimerRef.current);
		if (openRef.current) return;
		if (warm) show("none");
		else openTimerRef.current = setTimeout(() => show("enter"), FIRST_OPEN_DELAY_MS);
	}, [show]);

	// Off the row or off the card: a pending open is cancelled outright, a
	// showing preview closes after the grace unless the pointer reaches the
	// other half (row or card) first.
	const leave = useCallback(() => {
		clearTimeout(openTimerRef.current);
		if (!openRef.current) return;
		clearTimeout(leaveTimerRef.current);
		leaveTimerRef.current = setTimeout(hide, LEAVE_GRACE_MS);
	}, [hide]);

	const stay = useCallback(() => clearTimeout(leaveTimerRef.current), []);

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
			onPointerLeave: leave,
		}),
		[hide, leave, request],
	);
	const contentProps = useMemo(() => ({ onPointerEnter: stay, onPointerLeave: leave }), [leave, stay]);

	return { open: state.open && !suppressed, animation: state.animation, triggerProps, contentProps };
}
