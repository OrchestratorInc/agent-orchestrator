import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef } from "react";
import { pageHidden } from "../lib/page-hidden";

const SETTLE_PX = 0.5;
const OWN_SCROLL_PX = 2;
const FOLLOW_INSTANT_PX = 48;
const REAIM_PX = 48;
const FLIGHT_MAX_MS = 1500;

const CANNOT_ANIMATE = import.meta.env.MODE === "test";

type Flight = { from: number; to: number; aimedAt: number; deliberate: boolean };

export function useScrollFollow(getNode: () => HTMLElement | null) {
	const reducedMotion = useReducedMotion();
	const snap = useRef(false);
	snap.current = CANNOT_ANIMATE || Boolean(reducedMotion);
	const lastWritten = useRef<number | null>(null);
	const flight = useRef<Flight | null>(null);
	const lastAimAt = useRef(0);
	const holdUntil = useRef(0);

	const instant = () => snap.current || pageHidden();
	const endOf = (node: HTMLElement) => Math.max(0, node.scrollHeight - node.clientHeight);
	const write = (node: HTMLElement, top: number) => {
		if (Math.abs(node.scrollTop - top) <= SETTLE_PX) return;
		node.scrollTop = top;
		lastWritten.current = node.scrollTop;
	};
	const smoothTo = (node: HTMLElement, top: number, deliberate = flight.current?.deliberate ?? false) => {
		const now = performance.now();
		flight.current = { from: flight.current?.from ?? node.scrollTop, to: top, aimedAt: now, deliberate };
		lastAimAt.current = now;
		lastWritten.current = top;
		node.scrollTo({ top, behavior: "smooth" });
	};
	const flying = () => {
		const current = flight.current;
		if (current && performance.now() - current.aimedAt > FLIGHT_MAX_MS) flight.current = null;
		return flight.current != null;
	};

	const cancel = useCallback(() => {
		flight.current = null;
		if (performance.now() - lastAimAt.current > FLIGHT_MAX_MS) return;
		lastAimAt.current = 0;
		const node = getNode();
		if (!node) return;
		const top = node.scrollTop;
		node.scrollTop = top + 1;
		node.scrollTop = top;
		lastWritten.current = node.scrollTop;
	}, [getNode]);

	const hold = useCallback((ms: number) => {
		cancel();
		holdUntil.current = performance.now() + ms;
	}, [cancel]);

	const markWritten = useCallback((top: number) => {
		lastWritten.current = top;
	}, []);

	const isFlying = useCallback(() => flying(), []);

	const isGliding = useCallback(() => flying() && flight.current!.deliberate, []);

	const isOwnScroll = useCallback((top: number) => {
		const current = flying() ? flight.current : null;
		if (current) {
			const low = Math.min(current.from, current.to) - OWN_SCROLL_PX;
			const high = Math.max(current.from, current.to) + OWN_SCROLL_PX;
			if (top >= low && top <= high) return true;
		}
		return lastWritten.current != null && Math.abs(top - lastWritten.current) < OWN_SCROLL_PX;
	}, []);

	const followEnd = useCallback(() => {
		const node = getNode();
		if (!node || performance.now() < holdUntil.current) return;
		if (instant()) {
			flight.current = null;
			write(node, node.scrollHeight);
			return;
		}
		const end = endOf(node);
		if (node.scrollTop > end) {
			flight.current = null;
			write(node, end);
			return;
		}
		if (flying()) {
			if (Math.abs(flight.current!.to - end) > REAIM_PX) smoothTo(node, end);
			return;
		}
		const gap = end - node.scrollTop;
		if (gap <= SETTLE_PX) return;
		if (gap <= FOLLOW_INSTANT_PX) write(node, end);
		else smoothTo(node, end);
	}, [getNode]);
	const followEndRef = useRef(followEnd);
	followEndRef.current = followEnd;

	const glideToEnd = useCallback(() => {
		const node = getNode();
		if (!node) return;
		holdUntil.current = 0;
		if (instant()) {
			flight.current = null;
			write(node, node.scrollHeight);
			return;
		}
		smoothTo(node, endOf(node), true);
	}, [getNode]);

	const reaim = useCallback(() => {
		const node = getNode();
		if (!node || !flying()) return;
		if (instant()) {
			flight.current = null;
			write(node, node.scrollHeight);
			return;
		}
		if (Math.abs(flight.current!.to - endOf(node)) > REAIM_PX) smoothTo(node, endOf(node));
	}, [getNode]);

	const endFlight = useCallback(() => {
		const node = getNode();
		const current = flight.current;
		if (!node || !current) return;
		if (Math.abs(node.scrollTop - current.to) > OWN_SCROLL_PX && node.scrollTop < endOf(node) - OWN_SCROLL_PX) return;
		flight.current = null;
		followEndRef.current();
	}, [getNode]);

	useEffect(() => () => {
		flight.current = null;
	}, []);

	return { glideToEnd, followEnd, cancel, hold, markWritten, isOwnScroll, isFlying, isGliding, reaim, endFlight };
}
