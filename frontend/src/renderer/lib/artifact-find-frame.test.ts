import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import path from "node:path";
const engineSource = readFileSync(path.resolve(process.cwd(), "../backend/internal/renderpage/artifact_find.js"), "utf8");
const bootstrapSource = readFileSync(path.resolve(process.cwd(), "../backend/internal/renderpage/render_bootstrap.js"), "utf8");
import { foldExceptions } from "./artifact-find";
const disposeFrames: Array<() => void> = [];

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
	const message = async (method: string, params: Record<string, unknown>, source: Window = reader.parent, settle = true) => {
		reader.dispatchEvent(new MessageEvent("message", { source, data: { jsonrpc: "2.0", method, params } }));
		for (let i = 0; i < 8; i++) await Promise.resolve();
		if (settle && method === "ao/find" && sent.mock.calls.some(([message]) => message.method === "ao/find-started" && message.params.generation === params.generation)) {
			await vi.waitFor(() => expect(sent.mock.calls.some(([message]) => message.method === "ao/find-result" && message.params.generation === params.generation)).toBe(true));
		}
	};
	const configure = () => message("ao/find-configure", { exceptions: foldExceptions, bindings: [{ key: "f", ctrl: true, meta: false, shift: false, alt: false }], open: true });
	disposeFrames.push(() => { void message("ao/find-configure", { exceptions: foldExceptions, bindings: [], open: false }); });
	return { frame, reader, doc, sent, highlights, message, configure };
}
afterEach(() => { disposeFrames.splice(0).forEach((dispose) => dispose()); document.body.replaceChildren(); vi.restoreAllMocks(); vi.useRealTimers(); });

describe("local artifact sandbox find bridge", () => {
	it("requires host configuration and searches without returning document text", async () => {
		const h = frameHarness();
		await h.message("ao/find", { generation: 1, query: "hello" });
		expect(h.sent.mock.calls.some(([message]) => message.method === "ao/find-result")).toBe(false);
		await h.configure(); await h.message("ao/find", { generation: 2, query: "HELLO", active: 1, reveal: false });
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 2, total: 2, active: 1 } }, "*");
		expect(h.highlights.get("artifact-find-active").values().next().value.toString()).toBe("hello");
		await h.message("ao/find", { generation: 3, query: "hello world", active: 0, reveal: false });
		expect(h.highlights.get("artifact-find-active").values().next().value.toString()).toBe("Hello world");
		await h.message("ao/find", { generation: 4, query: "secret", active: 0, reveal: false });
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 4, total: 0, active: 0 } }, "*");
	});
	it("ignores messages that did not come from its parent", async () => {
		const h = frameHarness(); await h.configure();
		await h.message("ao/find", { generation: 1, query: "hello" }, h.reader);
		expect(h.sent.mock.calls.some(([message]) => message.method === "ao/find-result")).toBe(false);
	});
	it("retains the selected occurrence after the host refreshes sorted content", async () => {
		const h = frameHarness(); await h.configure();
		h.doc.body.innerHTML = "<p>hello first</p><p>hello second</p>";
		await h.message("ao/find", { generation: 1, query: "hello", active: 1, reveal: false });
		h.doc.body.prepend(h.doc.body.lastElementChild!);
		await h.message("ao/find", { generation: 1, query: "", active: 0 });
		await h.message("ao/find", { generation: 2, query: "hello", active: 1, reveal: false });
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 2, total: 2, active: 0 } }, "*");
		expect(h.highlights.get("artifact-find-active").values().next().value.startContainer.parentElement.textContent).toBe("hello second");
	});
	it("forwards the configured shortcut, preserves input commands, and clears highlights", async () => {
		const h = frameHarness(); await h.configure();
		const key = () => new KeyboardEvent("keydown", { key: "f", ctrlKey: true, bubbles: true, cancelable: true });
		h.doc.body.dispatchEvent(key()); expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-open", params: {} }, "*");
		h.sent.mockClear(); h.doc.querySelector("input")!.dispatchEvent(key()); expect(h.sent).not.toHaveBeenCalled();
		await h.message("ao/find", { generation: 1, query: "hello", active: 0, reveal: false });
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-close", params: {} }, "*");
		await h.message("ao/find", { generation: 2, query: "", active: 0 }); expect(h.highlights.size).toBe(0);
	});
	it("notifies the host after dynamically rendered text changes", async () => {
		vi.useFakeTimers();
		const h = frameHarness(); await h.configure(); await h.message("ao/find", { generation: 1, query: "hello", active: 0, reveal: false });
		h.sent.mockClear(); h.doc.querySelector("p")!.textContent = "new text";
		await vi.advanceTimersByTimeAsync(200);
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-changed", params: { generation: 1 } }, "*");
	});
	it("forwards Escape even before a query is entered", async () => {
		const h = frameHarness(); await h.configure();
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		expect(h.sent).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/find-close", params: {} }, "*");
	});
	it("stops shortcuts, highlights and queued changes when a reader is deactivated", async () => {
		vi.useFakeTimers();
		const h = frameHarness(); await h.configure();
		await h.message("ao/find", { generation: 1, query: "hello", active: 0, reveal: false });
		h.doc.querySelector("p")!.textContent = "changed";
		await vi.advanceTimersByTimeAsync(0);
		await h.message("ao/find-configure", { exceptions: foldExceptions, bindings: [], open: false });
		await h.message("ao/find", { generation: 2, query: "", active: 0, reveal: false });
		h.sent.mockClear();
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "f", ctrlKey: true, bubbles: true }));
		h.doc.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
		await vi.advanceTimersByTimeAsync(200);
		expect(h.sent.mock.calls.filter(([message]) => message.method.startsWith("ao/find"))).toEqual([]);
		expect(h.highlights.size).toBe(0);
	});
	it("yields Find and Escape to shadow-root editors and terminals", async () => {
		const h = frameHarness(); await h.configure();
		const host = h.doc.createElement("x-editor"); h.doc.body.append(host);
		const shadow = host.attachShadow({ mode: "open" });
		shadow.innerHTML = '<input><div class="cm-editor" tabindex="0"></div><div class="xterm" tabindex="0"></div><p>readable</p>';
		for (const editor of shadow.querySelectorAll("input,.cm-editor,.xterm")) {
			for (const key of ["f", "Escape"]) {
				h.sent.mockClear();
				const event = new KeyboardEvent("keydown", { key, ctrlKey: key === "f", bubbles: true, composed: true, cancelable: true });
				editor.dispatchEvent(event);
				expect(event.defaultPrevented).toBe(false); expect(h.sent).not.toHaveBeenCalled();
			}
		}
		shadow.querySelector("p")!.dispatchEvent(new KeyboardEvent("keydown", { key: "f", ctrlKey: true, bubbles: true, composed: true }));
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-open", params: {} }, "*");
	});
	it("refreshes results after shadow mutations and slot reassignment", async () => {
		vi.useFakeTimers();
		const h = frameHarness();
		h.doc.body.innerHTML = '<x-card><span slot="a">needle</span></x-card>';
		const host = h.doc.querySelector("x-card")!, shadow = host.attachShadow({ mode: "open" });
		shadow.innerHTML = '<p><slot name="a"></slot><span>needle</span></p>';
		await h.configure(); await h.message("ao/find", { generation: 10, query: "needle", active: 0, reveal: false });
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-result", params: { generation: 10, total: 2, active: 0 } }, "*");
		await vi.advanceTimersByTimeAsync(200); h.sent.mockClear();
		shadow.querySelector("span")!.textContent = "changed";
		await vi.advanceTimersByTimeAsync(150);
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-changed", params: { generation: 10 } }, "*");
		await h.message("ao/find", { generation: 11, query: "needle", active: 0, reveal: false });
		h.sent.mockClear(); host.querySelector("span")!.slot = "unassigned";
		await vi.advanceTimersByTimeAsync(150);
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-changed", params: { generation: 11 } }, "*");
	});
	it("acknowledges a search before completing it and rejects late cancelled work", async () => {
		vi.useFakeTimers();
		const h = frameHarness(); await h.configure();
		let clock = 0; vi.spyOn(performance, "now").mockImplementation(() => clock += 2);
		h.doc.body.innerHTML = `<pre>${"needle ".repeat(2000)}</pre>`;
		await h.message("ao/find", { generation: 20, query: "needle", active: 0, reveal: false }, h.reader.parent, false);
		expect(h.sent).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ao/find-started", params: { generation: 20 } }, "*");
		await h.message("ao/find-cancel", { generation: 21 });
		await vi.advanceTimersByTimeAsync(200);
		expect(h.sent.mock.calls.some(([message]) => message.method === "ao/find-result")).toBe(false);
		expect(h.highlights.size).toBe(0);
	});
});
