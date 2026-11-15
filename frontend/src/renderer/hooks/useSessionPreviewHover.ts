import { useCallback, useEffect, useMemo, useRef, useState, type FocusEvent, type PointerEvent } from "react";

const FIRST_OPEN_DELAY_MS = 150;
const COOL_DOWN_MS = 300;
const LEAVE_GRACE_MS = 60;
const AIM_GRACE_MS = 300;
const AIM_APEX_BUFFER_PX = 4;
const AIM_BACKOFF_PX = 2;

let warm = false;
let coolTimer: ReturnType<typeof setTimeout> | undefined;
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
		endAim();
		return;
	}
	if (!inTriangle(p, aim.triangle)) {
		aim.release();
		return;
	}
	const distance = distanceToBox(p, aim.card);
	if (distance > aim.closest + AIM_BACKOFF_PX) {
		aim.release();
		return;
	}
	if (distance >= aim.closest) return;
	aim.closest = distance;
	clearTimeout(aim.timer);
	aim.timer = setTimeout(aim.release, AIM_GRACE_MS);
}

function claimFromRadix(event: PointerEvent) {
	event.preventDefault();
}

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

export function resetSessionPreviewWarmth() {
	clearTimeout(coolTimer);
	warm = false;
	activeHide = undefined;
	endAim();
}

export type SessionPreviewAnimation = "enter" | "none";

function isFromRow(event: FocusEvent | PointerEvent) {
	return event.currentTarget.contains(event.target as Node);
}

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
			onBlur: (event: FocusEvent) => {
				if (event.relatedTarget instanceof Element && event.relatedTarget.closest("[data-session-preview-card]")) return;
				hide();
			},
			onFocus: (event: FocusEvent) => {
				if (isFromRow(event) && event.relatedTarget) request();
			},
			onPointerEnter: (event: PointerEvent) => {
				claimFromRadix(event);
				if (!isFromRow(event)) return;
				hoveredRow = event.currentTarget;
				if (event.pointerType === "touch") return;
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
