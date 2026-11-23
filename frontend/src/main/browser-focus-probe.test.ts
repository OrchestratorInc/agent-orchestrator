import { runInNewContext } from "node:vm";
import { describe, expect, it } from "vitest";
import { FOCUS_QUERY_SOURCE, FOCUS_VALUE_MAX, parseFocusPayload } from "./browser-focus-probe";

describe("parseFocusPayload", () => {
	it("keeps a field number, and drops one that is not a small whole number", () => {
		expect(parseFocusPayload(JSON.stringify({ kind: "text", value: "", field: 3 }))).toEqual({ kind: "text", value: "", field: "3" });
		expect(parseFocusPayload(JSON.stringify({ kind: "password", field: 4 }))).toEqual({ kind: "password", field: "4" });
		expect(parseFocusPayload(JSON.stringify({ kind: "text", field: "x" }))).toEqual({ kind: "text" });
		expect(parseFocusPayload(JSON.stringify({ kind: "text", field: 1.5 }))).toEqual({ kind: "text" });
	});

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

// The page's isolated world outlives each query, so its field numbers do too.
const world = (): Record<string, unknown> => ({ WeakMap, document: { activeElement: null } });
const query = (active: FakeElement | null, context = world()): Record<string, unknown> | null => {
	(context.document as { activeElement: unknown }).activeElement = active;
	const focused = JSON.parse(runInNewContext(FOCUS_QUERY_SOURCE, context) as string) as Record<string, unknown> | null;
	if (focused) delete focused.field;
	return focused;
};
const fieldOf = (active: FakeElement, context: Record<string, unknown>): unknown => {
	(context.document as { activeElement: unknown }).activeElement = active;
	return (JSON.parse(runInNewContext(FOCUS_QUERY_SOURCE, context) as string) as { field?: unknown }).field;
};

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

	// Two password fields report the same kind and no value: only this tells
	// the phone that focus moved from one to the other.
	it("numbers each field, and keeps its number while it stays focused", () => {
		const context = world();
		const password = { tagName: "INPUT", type: "password", value: "a" };
		const confirm = { tagName: "INPUT", type: "password", value: "" };
		const first = fieldOf(password, context);
		expect(typeof first).toBe("number");
		expect(fieldOf(password, context)).toBe(first);
		expect(fieldOf(confirm, context)).not.toBe(first);
		expect(fieldOf(password, context)).toBe(first);
	});

	it("follows focus into shadow roots and same-origin frames, not other origins", () => {
		expect(query({ tagName: "MY-FIELD", shadowRoot: { activeElement: { tagName: "INPUT", type: "email", value: "a@b" } } })).toEqual({ kind: "email", value: "a@b" });
		expect(query({ tagName: "IFRAME", contentDocument: { activeElement: { tagName: "INPUT", type: "tel", value: "1" } } })).toEqual({ kind: "tel", value: "1" });
		const crossOrigin = { tagName: "IFRAME", get contentDocument(): never { throw new DOMException("Blocked a frame with origin", "SecurityError"); } };
		expect(query(crossOrigin)).toBeNull();
	});
});
