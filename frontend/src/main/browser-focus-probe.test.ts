import { runInNewContext } from "node:vm";
import { describe, expect, it } from "vitest";
import { FOCUS_PROBE_SOURCE, FOCUS_VALUE_MAX, parseFocusPayload } from "./browser-focus-probe";

describe("parseFocusPayload", () => {
	it("accepts each editable kind with its value", () => {
		for (const kind of ["text", "email", "number", "url", "search", "tel", "textarea", "contenteditable"]) {
			expect(parseFocusPayload(JSON.stringify({ kind, value: "hi" }))).toEqual({ kind, value: "hi" });
		}
	});

	it("reports a blur as null", () => {
		expect(parseFocusPayload("null")).toBeNull();
	});

	it("never carries a password field's value", () => {
		expect(parseFocusPayload(JSON.stringify({ kind: "password", value: "hunter2" }))).toEqual({ kind: "password" });
	});

	it("drops a value over the cap and anything that is not a string", () => {
		expect(parseFocusPayload(JSON.stringify({ kind: "text", value: "x".repeat(FOCUS_VALUE_MAX + 1) }))).toEqual({ kind: "text" });
		expect(parseFocusPayload(JSON.stringify({ kind: "text", value: 5 }))).toEqual({ kind: "text" });
	});

	it("ignores malformed payloads and unknown kinds", () => {
		expect(parseFocusPayload("{")).toBeUndefined();
		expect(parseFocusPayload(JSON.stringify({ kind: "checkbox" }))).toBeUndefined();
		expect(parseFocusPayload(JSON.stringify([1]))).toBeUndefined();
		expect(parseFocusPayload(JSON.stringify("text"))).toBeUndefined();
	});
});

type FakeElement = Record<string, unknown> & { tagName: string };

function runProbe(active: () => FakeElement | null) {
	const listeners = new Map<string, () => void>();
	const reports: unknown[] = [];
	const timers: Array<() => void> = [];
	const context: Record<string, unknown> = {
		document: {
			get activeElement() { return active(); },
			addEventListener: (type: string, listener: () => void) => listeners.set(type, listener),
		},
		__aoFocus: (payload: string) => reports.push(JSON.parse(payload)),
		setTimeout: (fn: () => void) => timers.push(fn),
	};
	context.window = context;
	runInNewContext(FOCUS_PROBE_SOURCE, context);
	return {
		reports,
		focusin: () => listeners.get("focusin")?.(),
		input: () => listeners.get("input")?.(),
		focusout: () => { listeners.get("focusout")?.(); timers.splice(0).forEach((fn) => fn()); },
		reinstall: () => runInNewContext(FOCUS_PROBE_SOURCE, context),
	};
}

describe("focus probe", () => {
	it("reports the focused field's kind and value, and never a password's value", () => {
		let active: FakeElement | null = { tagName: "BODY" };
		const probe = runProbe(() => active);
		expect(probe.reports).toEqual([null]);

		active = { tagName: "INPUT", type: "password", value: "hunter2" };
		probe.focusin();
		active = { tagName: "TEXTAREA", value: "notes" };
		probe.focusin();
		active = { tagName: "INPUT", type: "search", value: "q" };
		probe.focusin();
		active = { tagName: "DIV", isContentEditable: true, innerText: "doc" };
		probe.focusin();
		expect(probe.reports.slice(1)).toEqual([
			{ kind: "password" },
			{ kind: "textarea", value: "notes" },
			{ kind: "search", value: "q" },
			{ kind: "contenteditable", value: "doc" },
		]);
		expect(JSON.stringify(probe.reports)).not.toContain("hunter2");
	});

	it("reports null when focus leaves editables, and skips fields that take no typing", () => {
		let active: FakeElement | null = { tagName: "INPUT", type: "text", value: "" };
		const probe = runProbe(() => active);
		active = { tagName: "INPUT", type: "checkbox", value: "on" };
		probe.focusout();
		active = { tagName: "INPUT", type: "text", value: "", readOnly: true };
		probe.focusin();
		expect(probe.reports).toEqual([{ kind: "text", value: "" }, null]);
	});

	// The frame's own probe reports focus inside it; its parent must not
	// overwrite that with null.
	it("leaves focus inside a child frame to that frame's probe", () => {
		let active: FakeElement | null = { tagName: "INPUT", type: "text", value: "a" };
		const probe = runProbe(() => active);
		active = { tagName: "IFRAME" };
		probe.focusout();
		expect(probe.reports).toEqual([{ kind: "text", value: "a" }]);
	});

	it("follows focus into shadow roots", () => {
		const probe = runProbe(() => ({ tagName: "MY-FIELD", shadowRoot: { activeElement: { tagName: "INPUT", type: "email", value: "a@b" } } }));
		expect(probe.reports).toEqual([{ kind: "email", value: "a@b" }]);
	});

	it("keeps the reported value current as the field changes", () => {
		const field = { tagName: "INPUT", type: "text", value: "a" };
		const probe = runProbe(() => field);
		field.value = "ab";
		probe.input();
		probe.input();
		expect(probe.reports).toEqual([{ kind: "text", value: "a" }, { kind: "text", value: "ab" }]);
	});

	it("installs once per document", () => {
		const probe = runProbe(() => ({ tagName: "BODY" }));
		probe.reinstall();
		expect(probe.reports).toEqual([null]);
	});
});
