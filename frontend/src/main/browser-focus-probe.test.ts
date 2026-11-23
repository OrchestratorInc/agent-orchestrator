import { runInNewContext } from "node:vm";
import { describe, expect, it } from "vitest";
import { FOCUS_QUERY_SOURCE, FOCUS_VALUE_MAX, parseFocusPayload } from "./browser-focus-probe";

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

const query = (active: FakeElement | null): unknown =>
	JSON.parse(runInNewContext(FOCUS_QUERY_SOURCE, { document: { activeElement: active } }) as string);

describe("focus query", () => {
	it("reports the focused field's kind and value, and never a password's value", () => {
		expect(query({ tagName: "INPUT", type: "password", value: "hunter2" })).toEqual({ kind: "password" });
		expect(query({ tagName: "TEXTAREA", value: "notes" })).toEqual({ kind: "textarea", value: "notes" });
		expect(query({ tagName: "INPUT", type: "search", value: "q" })).toEqual({ kind: "search", value: "q" });
		expect(query({ tagName: "DIV", isContentEditable: true, innerText: "doc" })).toEqual({ kind: "contenteditable", value: "doc" });
	});

	it("reports null for nothing focused and fields that take no typing", () => {
		expect(query(null)).toBeNull();
		expect(query({ tagName: "BODY" })).toBeNull();
		expect(query({ tagName: "INPUT", type: "checkbox", value: "on" })).toBeNull();
		expect(query({ tagName: "INPUT", type: "text", value: "", readOnly: true })).toBeNull();
	});

	it("leaves out a value over the cap", () => {
		expect(query({ tagName: "INPUT", type: "text", value: "x".repeat(FOCUS_VALUE_MAX + 1) })).toEqual({ kind: "text" });
	});

	it("follows focus into shadow roots and same-origin frames, not other origins", () => {
		expect(query({ tagName: "MY-FIELD", shadowRoot: { activeElement: { tagName: "INPUT", type: "email", value: "a@b" } } })).toEqual({ kind: "email", value: "a@b" });
		expect(query({ tagName: "IFRAME", contentDocument: { activeElement: { tagName: "INPUT", type: "tel", value: "1" } } })).toEqual({ kind: "tel", value: "1" });
		const crossOrigin = { tagName: "IFRAME", get contentDocument(): never { throw new Error("blocked"); } };
		expect(query(crossOrigin)).toBeNull();
	});
});
