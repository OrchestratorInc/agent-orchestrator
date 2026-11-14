import { useCallback, useEffect, useMemo, useRef, useState, type FocusEvent, type PointerEvent } from "react";

// The first preview waits out a short hover and fades in. Once one is showing,
// sweeping across the other rows swaps previews instantly with no animation,
// until the pointer has been off every row for COOL_DOWN_MS.
const FIRST_OPEN_DELAY_MS = 150;
const COOL_DOWN_MS = 300;
// Time to cross the gap from the row to its card (or back) without closing it.
const LEAVE_GRACE_MS = 60;
// How long the pointer may sit in the safe triangle without getting closer to
// the card. Amazon's jQuery-menu-aim and Radix's submenu pointer grace both use 300.
const AIM_GRACE_MS = 300;
// Pushes the apex back from the exit point so the first moves off the row land
// inside the triangle (Floating UI's safePolygon buffer).
const AIM_APEX_BUFFER_PX = 4;
// Slack for hand jitter before a move counts as backing away from the card.
const AIM_BACKOFF_PX = 2;

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

type Point = { x: number; y: number };
type Box = { left: number; right: number; top: number; bottom: number };
export type Triangle = readonly [Point, Point, Point];

/**
 * The safe triangle from where the pointer left the row to the card's facing
 * edge: its apex is the exit point, its base the two card corners on that edge.
 * Works for a card on any side. Undefined when the exit point is on the card.
 */
export function safeTriangle(exit: Point, card: Box): Triangle | undefined {
	const { left, right, top, bottom } = card;
	const b = AIM_APEX_BUFFER_PX;
	if (exit.x <= left) return [{ x: exit.x - b, y: exit.y }, { x: left, y: top }, { x: left, y: bottom }];
	if (exit.x >= right) return [{ x: exit.x + b, y: exit.y }, { x: right, y: top }, { x: right, y: bottom }];
	if (exit.y <= top) return [{ x: exit.x, y: exit.y - b }, { x: left, y: top }, { x: right, y: top }];
	if (exit.y >= bottom) return [{ x: exit.x, y: exit.y + b }, { x: left, y: bottom }, { x: right, y: bottom }];
	return undefined;
}

export function inTriangle(p: Point, [a, b, c]: Triangle): boolean {
	const cross = (u: Point, v: Point) => (v.x - u.x) * (p.y - u.y) - (v.y - u.y) * (p.x - u.x);
	const d1 = cross(a, b);
	const d2 = cross(b, c);
	const d3 = cross(c, a);
	return !((d1 < 0 || d2 < 0 || d3 < 0) && (d1 > 0 || d2 > 0 || d3 > 0));
}

function inBox(p: Point, box: Box) {
	return p.x >= box.left && p.x <= box.right && p.y >= box.top && p.y <= box.bottom;
}

function distanceToBox(p: Point, box: Box) {
	return Math.hypot(Math.max(box.left - p.x, 0, p.x - box.right), Math.max(box.top - p.y, 0, p.y - box.bottom));
}

// The pointer on its way from a row to that row's card. While it lasts, rows it
// crosses do not open their own previews. Driven by the shared pointermove
// listener, with no React state per move.
type Aim = {
	owner: () => void;
	card: Box;
	triangle: Triangle;
	closest: number;
	timer: ReturnType<typeof setTimeout>;
	release: () => void;
};
let aim: Aim | undefined;

function endAim() {
	if (!aim) return;
	clearTimeout(aim.timer);
	aim = undefined;
}

function moveAim(p: Point) {
	if (!aim) return;
	if (inBox(p, aim.card)) {
		// On the card: its own pointerenter keeps it open from here.
		endAim();
		return;
	}
	if (!inTriangle(p, aim.triangle)) {
		aim.release();
		return;
	}
	const distance = distanceToBox(p, aim.card);
	// Backing away from the card is not aiming at it.
	if (distance > aim.closest + AIM_BACKOFF_PX) {
		aim.release();
		return;
	}
	if (distance >= aim.closest) return;
	// Still closing in: restart the grace. Stalling or drifting lets it run out.
	aim.closest = distance;
	clearTimeout(aim.timer);
	aim.timer = setTimeout(aim.release, AIM_GRACE_MS);
}

// Radix's HoverCard trigger and content run their own pointer enter/leave
// timers and report a close through onOpenChange after closeDelay, which would
// shut the card the moment the pointer left the row. This hook owns hover, so
// it marks those events handled; Radix skips its handler for a prevented event.
// Escape and outside presses still close through Radix.
function claimFromRadix(event: PointerEvent) {
	event.preventDefault();
}

// Rows resolve under a still pointer while the list scrolls: the browser only
// sends pointerenter once the mouse moves, so the scroll handler looks up the
// row under the last pointer position itself.
type RowController = { request: () => void; leave: () => void };
const rowControllers = new WeakMap<Element, RowController>();
let hoveredRow: Element | undefined;
let lastPointer: { x: number; y: number } | undefined;
let listening = false;

function listenForScroll() {
	if (listening || typeof window === "undefined") return;
	listening = true;
	window.addEventListener(
		"pointermove",
		(event) => {
			lastPointer = { x: event.clientX, y: event.clientY };
			if (aim && event.pointerType !== "touch") moveAim(lastPointer);
		},
		{ capture: true, passive: true },
	);
	window.addEventListener(
		"scroll",
		(event) => {
			if (!lastPointer || aim || !(warm || activeHide)) return;
			const scroller = event.target;
			if (!(scroller instanceof Element)) return;
			const under = document.elementFromPoint(lastPointer.x, lastPointer.y);
			// Only the scrolled list decides; a pointer resting on the card stays put.
			if (!under || !scroller.contains(under)) return;
			const row = under.closest("[data-session-preview-row]") ?? undefined;
			if (row === hoveredRow) return;
			if (hoveredRow) rowControllers.get(hoveredRow)?.leave();
			hoveredRow = row;
			if (row) rowControllers.get(row)?.request();
		},
		{ capture: true, passive: true },
	);
}

/** Test hook: start the next hover from cold. */
export function resetSessionPreviewWarmth() {
	clearTimeout(coolTimer);
	warm = false;
	activeHide = undefined;
	endAim();
}

export type SessionPreviewAnimation = "enter" | "none";

// A portal stays a React child of the row (the archive confirm does), so its
// focus and pointer events reach the row's handlers. Only the row's own DOM asks
// for a preview; otherwise the confirm opens the card on top of itself.
function isFromRow(event: FocusEvent | PointerEvent) {
	return event.currentTarget.contains(event.target as Node);
}

/**
 * Open state for one row's hover preview. The row owns its pointer and focus
 * events through `triggerProps`, so leaving a row always cancels a pending
 * open: a controlled Radix card never reports a close for a card that is not
 * open yet, which would leave the delayed open running after the pointer left.
 *
 * The card itself is hoverable (its PR links are clickable), so a showing
 * preview survives a short LEAVE_GRACE_MS off the row and stays open while the
 * pointer is on the card. Spread `contentProps` on the card for that.
 *
 * Heading for the card diagonally crosses other rows. Leaving the row starts a
 * safe triangle from the exit point to the card's near edge: while the pointer
 * stays inside it and keeps closing in, the card stays open and crossed rows
 * do not open theirs. Leaving the triangle, or AIM_GRACE_MS without progress,
 * hands over to whatever row is under the pointer, or closes the card.
 */
export function useSessionPreviewHover(suppressed: boolean) {
	const [state, setState] = useState<{ open: boolean; animation: SessionPreviewAnimation }>({
		open: false,
		animation: "enter",
	});
	const openRef = useRef(false);
	const openTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const leaveTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const cardRef = useRef<HTMLElement | null>(null);

	const hide = useCallback(() => {
		clearTimeout(openTimerRef.current);
		clearTimeout(leaveTimerRef.current);
		if (aim?.owner === hide) endAim();
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
		if (aim?.owner === hide) endAim();
		if (openRef.current) return;
		if (warm) show("none");
		else openTimerRef.current = setTimeout(() => show("none"), FIRST_OPEN_DELAY_MS);
	}, [hide, show]);

	// Off the row or off the card: a pending open is cancelled outright, a
	// showing preview closes after the grace unless the pointer reaches the
	// other half (row or card) first.
	const leave = useCallback(() => {
		clearTimeout(openTimerRef.current);
		if (!openRef.current) return;
		clearTimeout(leaveTimerRef.current);
		leaveTimerRef.current = setTimeout(hide, LEAVE_GRACE_MS);
	}, [hide]);

	const stay = useCallback(() => {
		clearTimeout(leaveTimerRef.current);
		if (aim?.owner === hide) endAim();
	}, [hide]);

	// Off the row toward the showing card: hold it open while the pointer aims at
	// it. False when there is no card to aim at, so the caller leaves normally.
	const beginAim = useCallback(
		(exit: Point) => {
			const card = cardRef.current?.getBoundingClientRect();
			if (!openRef.current || !card || card.width === 0) return false;
			const triangle = safeTriangle(exit, card);
			if (!triangle) return false;
			endAim();
			clearTimeout(openTimerRef.current);
			clearTimeout(leaveTimerRef.current);
			const release = () => {
				endAim();
				const next = hoveredRow ? rowControllers.get(hoveredRow) : undefined;
				// Another row under the pointer takes over at once, as in a sweep.
				if (next && next.request !== request) next.request();
				else leave();
			};
			aim = {
				owner: hide,
				card: { left: card.left, right: card.right, top: card.top, bottom: card.bottom },
				triangle,
				closest: distanceToBox(exit, card),
				timer: setTimeout(release, AIM_GRACE_MS),
				release,
			};
			return true;
		},
		[hide, leave, request],
	);

	// A drag in progress closes the preview and keeps it from coming back.
	useEffect(() => {
		if (suppressed) hide();
	}, [hide, suppressed]);
	useEffect(() => () => hide(), [hide]);

	useEffect(listenForScroll, []);
	const rowRef = useCallback(
		(node: HTMLElement | null) => {
			if (node) rowControllers.set(node, { request, leave });
		},
		[leave, request],
	);

	const triggerProps = useMemo(
		() => ({
			"data-session-preview-row": "",
			// Focus moving into the card (a PR link being clicked) must not close it
			// before the click lands; the card's own pointer handlers take over.
			onBlur: (event: FocusEvent) => {
				if (event.relatedTarget instanceof Element && event.relatedTarget.closest("[data-session-preview-card]")) return;
				hide();
			},
			// Focus moved here from another control previews the row. Focus from
			// nowhere is the archive confirm handing it back on close: with the pointer
			// elsewhere, nothing would ever close the card.
			onFocus: (event: FocusEvent) => {
				if (isFromRow(event) && event.relatedTarget) request();
			},
			onPointerEnter: (event: PointerEvent) => {
				claimFromRadix(event);
				if (!isFromRow(event)) return;
				hoveredRow = event.currentTarget;
				if (event.pointerType === "touch") return;
				// Crossed on the way to another row's card: that card keeps the pointer.
				if (aim && aim.owner !== hide) return;
				request();
			},
			onPointerLeave: (event: PointerEvent) => {
				claimFromRadix(event);
				if (hoveredRow === event.currentTarget) hoveredRow = undefined;
				if (event.pointerType !== "touch" && beginAim({ x: event.clientX, y: event.clientY })) return;
				leave();
			},
		}),
		[beginAim, hide, leave, request],
	);
	const setCard = useCallback((node: HTMLElement | null) => {
		cardRef.current = node;
	}, []);
	const contentProps = useMemo(
		() => ({
			"data-session-preview-card": "",
			ref: setCard,
			onPointerEnter: (event: PointerEvent) => {
				claimFromRadix(event);
				stay();
			},
			onPointerLeave: (event: PointerEvent) => {
				claimFromRadix(event);
				leave();
			},
		}),
		[leave, setCard, stay],
	);

	return { open: state.open && !suppressed, animation: state.animation, hide, triggerProps, contentProps, rowRef };
}
