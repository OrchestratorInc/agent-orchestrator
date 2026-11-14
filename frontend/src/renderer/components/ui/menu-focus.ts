import { createContext, useCallback, type PointerEvent } from "react";

let menuReturnTarget: HTMLElement | null = null;
let capturedForMenu: HTMLElement | null = null;
let pending: { borrower: Element; target: HTMLElement } | null = null;

export type MenuTeardownDetail = {
	menu: HTMLElement;
	trigger: HTMLElement | null;
};

type MenuTeardownListener = (detail: MenuTeardownDetail) => void;
const teardownListeners = new Set<MenuTeardownListener>();

export function onMenuTeardownComplete(listener: MenuTeardownListener): () => void {
	teardownListeners.add(listener);
	return () => {
		teardownListeners.delete(listener);
	};
}

function publishMenuTeardownComplete(menu: HTMLElement) {
	const detail: MenuTeardownDetail = { menu, trigger: menuReturnTarget };
	for (const listener of [...teardownListeners]) listener(detail);
}

export function useMenuReturnTarget<T extends HTMLElement>() {
	return useCallback((menu: T | null) => {
		if (!menu || capturedForMenu === menu) return;
		capturedForMenu = menu;

		const active = document.activeElement;
		menuReturnTarget =
			active instanceof HTMLElement && active !== document.body && !menu.contains(active)
				? active
				: null;

		queueMicrotask(() => {
			if (capturedForMenu !== menu || !menu.id) return;
			const trigger = menu.ownerDocument.querySelector<HTMLElement>(
				`[aria-controls="${CSS.escape(menu.id)}"]`,
			);
			if (trigger) menuReturnTarget = trigger;
		});
	}, []);
}

export function menuFocusReturnTarget(): HTMLElement | null {
	return menuReturnTarget?.isConnected ? menuReturnTarget : null;
}

export function keepFocusOnOpenedSurface(event: Event) {
	if (event.defaultPrevented) return;
	const active = document.activeElement;
	if (!active || active === document.body) return;
	const menu = event.currentTarget ?? event.target;
	if (menu instanceof Node && menu.contains(active)) return;
	event.preventDefault();
	returnFocusWhenSurfaceCloses(active);
}

function returnFocusWhenSurfaceCloses(borrower: Element) {
	if (!menuReturnTarget) return;
	pending = {
		borrower: borrower.closest("[role='dialog'], [role='alertdialog']") ?? borrower,
		target: menuReturnTarget,
	};
	document.addEventListener("focusout", handleFocusRelease, true);
}

function clearPending() {
	pending = null;
	document.removeEventListener("focusout", handleFocusRelease, true);
}

function handleFocusRelease(event: FocusEvent) {
	const current = pending;
	if (!current) {
		clearPending();
		return;
	}
	if (event.relatedTarget !== null) {
		if (!current.borrower.contains(event.relatedTarget as Node)) clearPending();
		return;
	}
	requestAnimationFrame(() =>
		requestAnimationFrame(() => {
			if (pending !== current) return;
			if (current.borrower.isConnected) return;
			clearPending();
			if (document.activeElement !== document.body) return;
			if (current.target.isConnected) current.target.focus();
		}),
	);
}

export function composeMenuCloseAutoFocus(handler?: (event: Event) => void) {
	return (event: Event) => {
		handler?.(event);
		keepFocusOnOpenedSurface(event);
		const menu = event.currentTarget ?? event.target;
		if (menu instanceof HTMLElement) publishMenuTeardownComplete(menu);
	};
}

export function skipFocusHopToSibling(event: PointerEvent<HTMLElement>) {
	const next = event.relatedTarget instanceof Element ? event.relatedTarget.closest('[role^="menuitem"]') : null;
	if (next && next !== event.currentTarget && next.closest('[role="menu"]') === event.currentTarget.closest('[role="menu"]')) {
		event.preventDefault();
	}
}

export const ShieldOutsideClick = createContext(false);

export function swallowClickAfter(event: CustomEvent<{ originalEvent: globalThis.PointerEvent }>) {
	if (event.defaultPrevented || event.detail.originalEvent.button !== 0) return;
	const listening = new AbortController();
	const swallow = (click: MouseEvent) => {
		click.preventDefault();
		click.stopPropagation();
	};
	const release = () => setTimeout(() => listening.abort());
	document.addEventListener("click", swallow, { capture: true, signal: listening.signal });
	document.addEventListener("pointerup", release, { capture: true, once: true, signal: listening.signal });
	document.addEventListener("pointercancel", release, { capture: true, once: true, signal: listening.signal });
}
