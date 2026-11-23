import type { BrowserFocusedEditable } from "./browser-live-types";

// The phone types into the desktop page only once it knows which kind of field
// has focus. The probe that tells it runs in its own isolated world, and its
// binding exists only there, so the page's scripts can neither see the probe
// nor forge a report. It needs no Runtime.enable, which bot managers detect.
export const FOCUS_WORLD = "ao-focus";
export const FOCUS_BINDING = "__aoFocus";
/** Longer values are left out; the phone then types from an empty field. */
export const FOCUS_VALUE_MAX = 4096;

const KINDS = new Set<BrowserFocusedEditable["kind"]>(["text", "password", "email", "number", "url", "search", "tel", "textarea", "contenteditable"]);

export const FOCUS_PROBE_SOURCE = `(() => {
	if (window.__aoFocusInstalled) return;
	window.__aoFocusInstalled = true;
	const report = globalThis.${FOCUS_BINDING};
	if (typeof report !== "function") return;
	const inputKinds = new Set(["text", "password", "email", "number", "url", "search", "tel"]);
	let last;
	const describe = (element) => {
		while (element && element.shadowRoot && element.shadowRoot.activeElement) element = element.shadowRoot.activeElement;
		if (!element) return null;
		if (element.tagName === "IFRAME" || element.tagName === "FRAME") return undefined;
		if (element.readOnly || element.disabled) return null;
		if (element.tagName === "TEXTAREA") return { kind: "textarea", value: String(element.value) };
		if (element.tagName === "INPUT") {
			const kind = String(element.type || "text").toLowerCase();
			if (!inputKinds.has(kind)) return null;
			return kind === "password" ? { kind } : { kind, value: String(element.value) };
		}
		if (element.isContentEditable) return { kind: "contenteditable", value: String(element.innerText) };
		return null;
	};
	const send = () => {
		const focused = describe(document.activeElement);
		if (focused === undefined) return;
		if (focused && focused.value !== undefined && focused.value.length > ${FOCUS_VALUE_MAX}) delete focused.value;
		const payload = JSON.stringify(focused);
		if (payload === last) return;
		last = payload;
		report(payload);
	};
	document.addEventListener("focusin", send, true);
	// Kept current, so a phone that reconnects starts from the page's text.
	document.addEventListener("input", send, true);
	// Focus moving between fields blurs the first before the second gains it.
	document.addEventListener("focusout", () => setTimeout(send, 0), true);
	send();
})();`;

/**
 * A probe report, or undefined for one that is malformed. Validated again
 * here: the probe runs inside the page's process.
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
	const { kind, value } = parsed as { kind?: unknown; value?: unknown };
	if (typeof kind !== "string" || !KINDS.has(kind as BrowserFocusedEditable["kind"])) return undefined;
	const focused: BrowserFocusedEditable = { kind: kind as BrowserFocusedEditable["kind"] };
	if (kind !== "password" && typeof value === "string" && value.length <= FOCUS_VALUE_MAX) focused.value = value;
	return focused;
}
