import type { BrowserFocusedEditable } from "./browser-live-types";

// The phone types into the desktop page only once it knows which kind of field
// has focus. The desktop asks the page with a read-only query in its own
// isolated world, which the page's scripts can neither see nor change. It
// asks rather than listens: a binding needs Runtime.enable, which bot
// managers detect, and a parked page gets no focus events.
export const FOCUS_WORLD = "ao-focus";
/** Longer values are left out; the phone then types from an empty field. */
export const FOCUS_VALUE_MAX = 4096;

const KINDS = new Set<BrowserFocusedEditable["kind"]>(["text", "password", "email", "number", "url", "search", "tel", "textarea", "contenteditable"]);

/** Evaluates to the focused field as JSON: the same shape parseFocusPayload reads. */
export const FOCUS_QUERY_SOURCE = `(() => {
	const inputKinds = new Set(["text", "password", "email", "number", "url", "search", "tel"]);
	let element = document.activeElement;
	for (;;) {
		if (element && element.shadowRoot && element.shadowRoot.activeElement) element = element.shadowRoot.activeElement;
		// A same-origin frame's document is readable; another origin's is not.
		else if (element && (element.tagName === "IFRAME" || element.tagName === "FRAME")) {
			let inner = null;
			try { inner = element.contentDocument && element.contentDocument.activeElement; } catch {}
			if (!inner) return "null";
			element = inner;
		} else break;
	}
	if (!element || element.readOnly || element.disabled) return "null";
	let focused = null;
	if (element.tagName === "TEXTAREA") focused = { kind: "textarea", value: String(element.value) };
	else if (element.tagName === "INPUT") {
		const kind = String(element.type || "text").toLowerCase();
		if (inputKinds.has(kind)) focused = kind === "password" ? { kind } : { kind, value: String(element.value) };
	} else if (element.isContentEditable) focused = { kind: "contenteditable", value: String(element.innerText) };
	if (focused && focused.value !== undefined && focused.value.length > ${FOCUS_VALUE_MAX}) delete focused.value;
	// Numbers each field in this world's own map, which the page cannot see, so
	// the phone can tell two fields of one kind apart.
	if (focused) {
		const fields = globalThis.__aoFocusFields || (globalThis.__aoFocusFields = { map: new WeakMap(), next: 0 });
		let field = fields.map.get(element);
		if (field === undefined) fields.map.set(element, field = ++fields.next);
		focused.field = field;
	}
	return JSON.stringify(focused);
})()`;

/**
 * A probe report, or undefined for one that is malformed. Validated again
 * here: the query runs inside the page's process.
 */
export function parseFocusPayload(payload: string): BrowserFocusedEditable | null | undefined {
	let parsed: unknown;
	try {
		parsed = JSON.parse(payload);
	} catch {
		return undefined;
	}
	if (parsed === null) return null;
	if (typeof parsed !== "object" || Array.isArray(parsed)) return undefined;
	const { kind, value, field } = parsed as { kind?: unknown; value?: unknown; field?: unknown };
	if (typeof kind !== "string" || !KINDS.has(kind as BrowserFocusedEditable["kind"])) return undefined;
	const focused: BrowserFocusedEditable = { kind: kind as BrowserFocusedEditable["kind"] };
	if (kind !== "password" && typeof value === "string" && value.length <= FOCUS_VALUE_MAX) focused.value = value;
	if (Number.isSafeInteger(field) && (field as number) > 0) focused.field = String(field);
	return focused;
}
