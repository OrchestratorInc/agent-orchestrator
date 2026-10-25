// Element operations the content script performs in the extension's isolated
// world. Trusted pointer and keyboard input happens in the service worker
// (debugger tier); everything here is DOM-level (the T0 tier) or read-only.

import { BridgeError } from "./protocol";
import { buildSnapshot, INTERACTIVE_ROLES, type RefRegistry, roleOf, accessibleName } from "./snapshot";

const MAX_TEXT = 50_000;

export type Geometry = { x: number; y: number; width: number; height: number; inViewport: boolean };

function htmlElement(element: Element): HTMLElement {
	return element as HTMLElement;
}

function assertEnabled(element: Element): void {
	if ((element as HTMLInputElement).disabled || element.getAttribute("aria-disabled") === "true") {
		throw new BridgeError("ELEMENT_DISABLED", "The element is disabled");
	}
}

/** Scroll the element to the viewport centre when needed and return its centre. */
export function geometry(element: Element): Geometry {
	const view = element.ownerDocument.defaultView!;
	let rect = element.getBoundingClientRect();
	const outside = rect.bottom < 0 || rect.right < 0 || rect.top > view.innerHeight || rect.left > view.innerWidth;
	if (outside || rect.width === 0) {
		htmlElement(element).scrollIntoView?.({ block: "center", inline: "center" });
		rect = element.getBoundingClientRect();
	}
	const inViewport = rect.bottom > 0 && rect.right > 0 && rect.top < view.innerHeight && rect.left < view.innerWidth;
	return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, width: rect.width, height: rect.height, inViewport };
}

function nativeValueSetter(element: HTMLInputElement | HTMLTextAreaElement): ((value: string) => void) | undefined {
	const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
	const descriptor = Object.getOwnPropertyDescriptor(prototype, "value");
	return descriptor?.set ? (value) => descriptor.set!.call(element, value) : undefined;
}

function dispatchInput(element: Element, data: string | null, inputType = "insertText"): void {
	const view = element.ownerDocument.defaultView!;
	const Input = view.InputEvent ?? view.Event;
	element.dispatchEvent(new Input("input", { bubbles: true, composed: true, data, inputType } as InputEventInit));
}

function isTextControl(element: Element): element is HTMLInputElement | HTMLTextAreaElement {
	return element instanceof element.ownerDocument.defaultView!.HTMLTextAreaElement ||
		(element instanceof element.ownerDocument.defaultView!.HTMLInputElement &&
			!["checkbox", "radio", "submit", "button", "reset", "image", "file", "range", "color", "hidden"].includes(
				(element.getAttribute("type") ?? "text").toLowerCase(),
			));
}

function isContentEditable(element: Element): boolean {
	const value = element.getAttribute("contenteditable");
	return value === "" || value === "true" || (element as HTMLElement).isContentEditable === true;
}

/** Replace a field's value, as `ao browser fill` does. */
export function fill(element: Element, text: string): void {
	assertEnabled(element);
	htmlElement(element).focus?.();
	if (isTextControl(element)) {
		if (element.readOnly) throw new BridgeError("ELEMENT_NOT_EDITABLE", "The field is read-only");
		// The prototype setter bypasses framework value trackers (React), so the
		// input event is seen as a real change.
		(nativeValueSetter(element) ?? ((value: string) => (element.value = value)))(text);
		dispatchInput(element, text);
		element.dispatchEvent(new (element.ownerDocument.defaultView!.Event)("change", { bubbles: true }));
		return;
	}
	if (isContentEditable(element)) {
		element.textContent = text;
		dispatchInput(element, text);
		return;
	}
	throw new BridgeError("ELEMENT_NOT_EDITABLE", "The element does not accept text");
}

/** Insert text at the cursor without trusted input (synthetic tier). */
export function typeText(element: Element, text: string): void {
	assertEnabled(element);
	htmlElement(element).focus?.();
	if (isTextControl(element)) {
		const start = element.selectionStart ?? element.value.length;
		const end = element.selectionEnd ?? start;
		const next = element.value.slice(0, start) + text + element.value.slice(end);
		(nativeValueSetter(element) ?? ((value: string) => (element.value = value)))(next);
		if (element.selectionStart !== null) element.setSelectionRange(start + text.length, start + text.length);
		dispatchInput(element, text);
		return;
	}
	if (isContentEditable(element)) {
		element.textContent = (element.textContent ?? "") + text;
		dispatchInput(element, text);
		return;
	}
	throw new BridgeError("ELEMENT_NOT_EDITABLE", "The element does not accept text");
}

/** Choose an option by value or visible label. */
export function selectOption(element: Element, value: string): string {
	assertEnabled(element);
	if (!(element instanceof element.ownerDocument.defaultView!.HTMLSelectElement)) {
		throw new BridgeError("INVALID_ARGUMENT", "The element is not a select");
	}
	const options = Array.from(element.options);
	const wanted = value.trim();
	const option =
		options.find((candidate) => candidate.value === value) ??
		options.find((candidate) => (candidate.textContent ?? "").trim() === wanted) ??
		options.find((candidate) => (candidate.textContent ?? "").trim().toLowerCase() === wanted.toLowerCase());
	if (!option) throw new BridgeError("OPTION_NOT_FOUND", `No option matches ${JSON.stringify(value)}`);
	element.value = option.value;
	option.selected = true;
	dispatchInput(element, null, "insertReplacementText");
	element.dispatchEvent(new (element.ownerDocument.defaultView!.Event)("change", { bubbles: true }));
	return option.value;
}

function checkedState(element: Element): boolean {
	if (element instanceof element.ownerDocument.defaultView!.HTMLInputElement) return element.checked;
	return element.getAttribute("aria-checked") === "true";
}

/** Set a checkbox, radio, or switch to the requested state. */
export function setChecked(element: Element, checked: boolean): boolean {
	assertEnabled(element);
	const role = roleOf(element);
	if (!["checkbox", "radio", "switch", "menuitemcheckbox", "menuitemradio"].includes(role)) {
		throw new BridgeError("INVALID_ARGUMENT", "The element is not checkable");
	}
	if (checkedState(element) !== checked) htmlElement(element).click();
	return checkedState(element);
}

/** Synthetic click (T0): pointer and mouse events, then click. */
export function clickSynthetic(element: Element, clicks = 1): void {
	assertEnabled(element);
	const view = element.ownerDocument.defaultView!;
	const { x, y } = geometry(element);
	const init = { bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y, button: 0 };
	const Pointer = view.PointerEvent ?? view.MouseEvent;
	for (let i = 1; i <= clicks; i++) {
		element.dispatchEvent(new Pointer("pointerdown", { ...init, detail: i }));
		element.dispatchEvent(new view.MouseEvent("mousedown", { ...init, detail: i }));
		htmlElement(element).focus?.();
		element.dispatchEvent(new Pointer("pointerup", { ...init, detail: i }));
		element.dispatchEvent(new view.MouseEvent("mouseup", { ...init, detail: i }));
		element.dispatchEvent(new view.MouseEvent("click", { ...init, detail: i }));
	}
	if (clicks === 2) element.dispatchEvent(new view.MouseEvent("dblclick", { ...init, detail: 2 }));
}

export function hoverSynthetic(element: Element): void {
	const view = element.ownerDocument.defaultView!;
	const { x, y } = geometry(element);
	const init = { bubbles: true, composed: true, clientX: x, clientY: y };
	const Pointer = view.PointerEvent ?? view.MouseEvent;
	element.dispatchEvent(new Pointer("pointerover", init));
	element.dispatchEvent(new view.MouseEvent("mouseover", init));
	element.dispatchEvent(new view.MouseEvent("mouseenter", { ...init, bubbles: false }));
	element.dispatchEvent(new view.MouseEvent("mousemove", init));
}

export function scrollPage(document: Document, direction: string, amount: number, element?: Element): { x: number; y: number } {
	const dx = direction === "left" ? -amount : direction === "right" ? amount : 0;
	const dy = direction === "up" ? -amount : direction === "down" ? amount : 0;
	if (dx === 0 && dy === 0) throw new BridgeError("INVALID_ARGUMENT", "direction must be up, down, left, or right");
	const view = document.defaultView!;
	if (element) htmlElement(element).scrollBy?.({ left: dx, top: dy });
	else view.scrollBy?.({ left: dx, top: dy });
	return { x: view.scrollX ?? 0, y: view.scrollY ?? 0 };
}

export function pageText(document: Document, element?: Element): string {
	const target = (element ?? document.body ?? document.documentElement) as HTMLElement | null;
	if (!target) return "";
	const text = typeof target.innerText === "string" ? target.innerText : (target.textContent ?? "");
	return text.length > MAX_TEXT ? `${text.slice(0, MAX_TEXT)}…` : text;
}

export function elementValue(element: Element): string {
	if (element.tagName === "INPUT" && (element.getAttribute("type") ?? "").toLowerCase() === "password") {
		throw new BridgeError("INVALID_ARGUMENT", "Password values are not readable");
	}
	if ("value" in element && typeof (element as HTMLInputElement).value === "string") return (element as HTMLInputElement).value;
	return element.textContent ?? "";
}

export function elementChecked(element: Element): boolean {
	return checkedState(element);
}

export type WaitKind = "text" | "text-gone" | "selector" | "selector-gone" | "dom-stable";

/**
 * Playwright's visibility rule, which the panel's agent-browser runtime uses:
 * a non-empty bounding box (so display:none and detached elements fail) and
 * not visibility:hidden/collapse.
 */
export function isVisible(element: Element): boolean {
	if (!element.isConnected) return false;
	const rect = element.getBoundingClientRect();
	if (rect.width <= 0 || rect.height <= 0) return false;
	const style = element.ownerDocument.defaultView?.getComputedStyle(element);
	return style?.visibility !== "hidden" && style?.visibility !== "collapse";
}

/** Whether a wait condition holds right now (dom-stable is handled by waitFor). */
export function conditionHolds(document: Document, kind: WaitKind, value: string): boolean {
	switch (kind) {
		case "text":
			return pageText(document).includes(value);
		case "text-gone":
			return !pageText(document).includes(value);
		case "selector":
		case "selector-gone": {
			let found: Element | null;
			try {
				found = document.querySelector(value);
			} catch {
				throw new BridgeError("INVALID_ARGUMENT", `Invalid selector: ${value}`);
			}
			// Same rule as the panel's runtime: a selector wait means usable on
			// screen, and "gone" means detached from the document.
			const visible = found !== null && isVisible(found);
			return kind === "selector" ? visible : !found;
		}
		default:
			return false;
	}
}

/** Wait for a condition, polling and watching DOM mutations. */
export function waitFor(document: Document, kind: WaitKind, value: string, timeoutMs: number): Promise<boolean> {
	return new Promise((resolve, reject) => {
		const view = document.defaultView!;
		let settled = false;
		let quietTimer: ReturnType<typeof setTimeout> | undefined;
		const finish = (result: boolean, error?: unknown) => {
			if (settled) return;
			settled = true;
			observer.disconnect();
			clearInterval(poll);
			clearTimeout(deadline);
			if (quietTimer) clearTimeout(quietTimer);
			if (error) reject(error);
			else resolve(result);
		};
		const check = () => {
			if (kind === "dom-stable") return;
			try {
				if (conditionHolds(document, kind, value)) finish(true);
			} catch (error) {
				finish(false, error);
			}
		};
		const quietMs = kind === "dom-stable" ? Math.max(50, Number(value) || 500) : 0;
		const armQuiet = () => {
			if (quietTimer) clearTimeout(quietTimer);
			quietTimer = setTimeout(() => finish(true), quietMs);
		};
		const observer = new view.MutationObserver(() => (kind === "dom-stable" ? armQuiet() : check()));
		observer.observe(document, { subtree: true, childList: true, characterData: true, attributes: true });
		const poll = setInterval(check, 100);
		const deadline = setTimeout(() => finish(false), timeoutMs);
		if (kind === "dom-stable") armQuiet();
		else check();
	});
}

let highlightHost: HTMLElement | undefined;

/** Draw a non-interactive outline around an element until cleared. */
export function highlight(element: Element): void {
	unhighlight(element.ownerDocument);
	const document = element.ownerDocument;
	const rect = element.getBoundingClientRect();
	const host = document.createElement("div");
	const root = host.attachShadow({ mode: "closed" });
	const box = document.createElement("div");
	box.setAttribute(
		"style",
		`position:fixed;left:${rect.left - 3}px;top:${rect.top - 3}px;width:${rect.width + 6}px;height:${rect.height + 6}px;` +
			"border:2px solid #f97316;border-radius:4px;box-shadow:0 0 0 4px rgba(249,115,22,.25);pointer-events:none;z-index:2147483647",
	);
	root.append(box);
	host.setAttribute("style", "all:initial;pointer-events:none");
	document.documentElement.append(host);
	highlightHost = host;
}

export function unhighlight(_document?: Document): void {
	highlightHost?.remove();
	highlightHost = undefined;
}

export type Annotation = { ref: string; role: string; name: string; rect: { x: number; y: number; width: number; height: number } };

/** Interactive elements visible in the viewport, for annotated screenshots. */
export function annotations(document: Document, registry: RefRegistry): Annotation[] {
	const view = document.defaultView!;
	const { refs } = buildSnapshot(document, registry, { interactive: true });
	const out: Annotation[] = [];
	for (const [ref, info] of Object.entries(refs)) {
		if (!INTERACTIVE_ROLES.has(info.role)) continue;
		let element: Element;
		try {
			element = registry.resolve(ref);
		} catch {
			continue;
		}
		const rect = element.getBoundingClientRect();
		if (rect.width <= 0 || rect.height <= 0) continue;
		if (rect.bottom < 0 || rect.right < 0 || rect.top > view.innerHeight || rect.left > view.innerWidth) continue;
		out.push({ ref, role: info.role, name: info.name, rect: { x: rect.left, y: rect.top, width: rect.width, height: rect.height } });
	}
	return out;
}

/** An iframe element's content-box offset within this frame's viewport. */
export function frameOffset(element: Element): { x: number; y: number; src: string; name: string } {
	if (element.tagName !== "IFRAME" && element.tagName !== "FRAME") {
		throw new BridgeError("INVALID_ARGUMENT", "The element is not a frame");
	}
	const rect = element.getBoundingClientRect();
	const style = element.ownerDocument.defaultView!.getComputedStyle(element);
	return {
		x: rect.left + (parseFloat(style.borderLeftWidth) || 0) + (parseFloat(style.paddingLeft) || 0),
		y: rect.top + (parseFloat(style.borderTopWidth) || 0) + (parseFloat(style.paddingTop) || 0),
		src: (element as HTMLIFrameElement).src,
		name: accessibleName(element),
	};
}

/** Synthetic key press (T0) on the focused element. */
export function pressSynthetic(document: Document, chord: string): void {
	const parts = chord.split("+").map((part) => part.trim()).filter(Boolean);
	const key = parts.pop();
	if (!key) throw new BridgeError("INVALID_ARGUMENT", "key is required");
	const modifiers = new Set(parts.map((part) => part.toLowerCase()));
	const view = document.defaultView!;
	const target = document.activeElement ?? document.body;
	const init = {
		key: key.length === 1 ? key : key === "Space" ? " " : key,
		bubbles: true,
		cancelable: true,
		composed: true,
		ctrlKey: modifiers.has("control") || modifiers.has("ctrl"),
		metaKey: modifiers.has("meta") || modifiers.has("cmd"),
		altKey: modifiers.has("alt"),
		shiftKey: modifiers.has("shift"),
	};
	target.dispatchEvent(new view.KeyboardEvent("keydown", init));
	target.dispatchEvent(new view.KeyboardEvent("keyup", init));
}
