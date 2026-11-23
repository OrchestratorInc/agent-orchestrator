import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import path from "node:path";
const engineSource = readFileSync(path.resolve(process.cwd(), "../backend/internal/renderpage/artifact_find.js"), "utf8");
const bootstrapSource = readFileSync(path.resolve(process.cwd(), "../backend/internal/renderpage/render_bootstrap.js"), "utf8");
import { foldExceptions } from "./artifact-find";

function frameHarness() {
	const frame = document.createElement("iframe"); document.body.append(frame);
	const reader = frame.contentWindow!, doc = frame.contentDocument!;
	doc.body.innerHTML = `<p>Hello <strong>world</strong> hello</p><p hidden>secret</p><input value="draft" />`;
	const sent = vi.spyOn(reader.parent, "postMessage");
	const highlights = new Map();
	Object.defineProperty(reader, "CSS", { configurable: true, value: { highlights } });
	Object.defineProperty(reader, "Highlight", { configurable: true, value: class extends Set<Range> {} });
	// Run precisely the sources embedded by renderpage.Document in a child realm.
	new Function("window", "document", "location", "history", engineSource.replace("export function", "function") + bootstrapSource)(reader, doc, reader.location, reader.history);
	const message = (method: string, params: Record<string, unknown>, source: Window = reader.parent) => reader.dispatchEvent(new MessageEvent("message", { source, data: { jsonrpc: "2.0", method, params } }));
	const configure = () => message("ao/find-configure", { exceptions: foldExceptions, bindings: [{ key: "f", ctrl: true, meta: false, shift: false, alt: false }], open: true });
	return { frame, reader, doc, sent, highlights, message, configure };
}
afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); vi.useRealTimers(); });

describe("local artifact sandbox find bridge", () => {
	it("requires host configuration and searches without returning document text", () => {
		const h = frameHarness();
		h.message("ao/find", { generation: 1, query: "hello" });
		expect(h.sent.mock.calls.some(([message]) => message.method === "ao/find-result")).toBe(false);
		h.configure(); h.message("ao/find", { generation: 2, query: "HELLO", active: 1, reveal: false });
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 2, total: 2, active: 1 } }, "*");
		expect(h.highlights.get("artifact-find-active").values().next().value.toString()).toBe("hello");
		h.message("ao/find", { generation: 3, query: "hello world", active: 0, reveal: false });
		expect(h.highlights.get("artifact-find-active").values().next().value.toString()).toBe("Hello world");
		h.message("ao/find", { generation: 4, query: "secret", active: 0, reveal: false });
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 4, total: 0, active: 0 } }, "*");
	});
	it("ignores messages that did not come from its parent", () => {
		const h = frameHarness(); h.configure();
		h.message("ao/find", { generation: 1, query: "hello" }, h.reader);
		expect(h.sent.mock.calls.some(([message]) => message.method === "ao/find-result")).toBe(false);
	});
	it("retains the selected occurrence after the host refreshes sorted content", () => {
		const h = frameHarness(); h.configure();
		h.doc.body.innerHTML = "<p>hello first</p><p>hello second</p>";
		h.message("ao/find", { generation: 1, query: "hello", active: 1, reveal: false });
		h.doc.body.prepend(h.doc.body.lastElementChild!);
		h.message("ao/find", { generation: 1, query: "", active: 0 });
		h.message("ao/find", { generation: 2, query: "hello", active: 1, reveal: false });
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 2, total: 2, active: 0 } }, "*");
		expect(h.highlights.get("artifact-find-active").values().next().value.startContainer.parentElement.textContent).toBe("hello second");
	});
	it("forwards the configured shortcut, preserves input commands, and clears highlights", () => {
		const h = frameHarness(); h.configure();
		const key = () => new KeyboardEvent("keydown", { key: "f", ctrlKey: true, bubbles: true, cancelable: true });
		h.doc.body.dispatchEvent(key()); expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-open", params: {} }, "*");
		h.sent.mockClear(); h.doc.querySelector("input")!.dispatchEvent(key()); expect(h.sent).not.toHaveBeenCalled();
		h.message("ao/find", { generation: 1, query: "hello", active: 0, reveal: false });
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-close", params: {} }, "*");
		h.message("ao/find", { generation: 2, query: "", active: 0 }); expect(h.highlights.size).toBe(0);
	});
	it("notifies the host after dynamically rendered text changes", async () => {
		vi.useFakeTimers();
		const h = frameHarness(); h.configure(); h.message("ao/find", { generation: 1, query: "hello", active: 0, reveal: false });
		h.sent.mockClear(); h.doc.querySelector("p")!.textContent = "new text";
		await vi.advanceTimersByTimeAsync(200);
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-changed", params: { generation: 1 } }, "*");
	});
	it("forwards Escape even before a query is entered", () => {
		const h = frameHarness(); h.configure();
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-close", params: {} }, "*");
	});
	it("stops shortcuts, highlights and queued changes when a reader is deactivated", async () => {
		vi.useFakeTimers();
		const h = frameHarness(); h.configure();
		h.message("ao/find", { generation: 1, query: "hello", active: 0, reveal: false });
		h.doc.querySelector("p")!.textContent = "changed";
		await vi.advanceTimersByTimeAsync(0);
		h.message("ao/find-configure", { exceptions: foldExceptions, bindings: [], open: false });
		h.message("ao/find", { generation: 2, query: "", active: 0, reveal: false });
		h.sent.mockClear();
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "f", ctrlKey: true, bubbles: true }));
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		await vi.advanceTimersByTimeAsync(200);
		expect(h.sent.mock.calls.filter(([message]) => message.method.startsWith("ao/find"))).toEqual([]);
		expect(h.highlights.size).toBe(0);
	});
});
