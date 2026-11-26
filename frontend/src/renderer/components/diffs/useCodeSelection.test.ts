import { act, fireEvent, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { codeReference, readCodeSelection, restoreCodeSelection, useCodeSelection } from "./useCodeSelection";

type Row = [line: number, type: string, text: string, alt?: number];

// Pierre's surface shape: a host whose shadow root holds one column per side.
function surface(columns: Array<{ side?: "deletions" | "additions"; rows: Row[] }>): HTMLElement {
	const host = document.createElement("diffs-container");
	const root = host.attachShadow({ mode: "open" });
	for (const { side, rows } of columns) {
		const code = document.createElement("div");
		code.setAttribute("data-code", "");
		if (side) code.setAttribute(`data-${side}`, "");
		const column = document.createElement("div");
		column.setAttribute("data-content", "");
		for (const [line, type, text, alt] of rows) {
			const row = document.createElement("div");
			row.setAttribute("data-line", String(line));
			row.setAttribute("data-line-type", type);
			if (alt != null) row.setAttribute("data-alt-line", String(alt));
			row.textContent = text;
			column.append(row);
		}
		code.append(column);
		root.append(code);
	}
	document.body.append(host);
	return host;
}

// jsdom has no shadow-root selection; stand in for Electron 33's, from the start
// of one row's text to `endOffset` characters into another's. Like Electron's,
// it calls itself collapsed and hands out a range on the host: only its anchor
// and focus points can be trusted.
function select(host: HTMLElement, from: Element, to: Element, endOffset: number, setBaseAndExtent?: (base: Node, baseOffset: number, extent: Node, extentOffset: number) => void) {
	const hostRange = document.createRange();
	hostRange.selectNode(host);
	Object.assign(host.shadowRoot!, {
		getSelection: () => ({
			isCollapsed: true,
			removeAllRanges: vi.fn(),
			rangeCount: 1,
			anchorNode: from.firstChild,
			anchorOffset: 0,
			focusNode: to.firstChild,
			focusOffset: endOffset,
			toString: () => "",
			getRangeAt: () => hostRange,
			...(setBaseAndExtent ? { setBaseAndExtent } : {}),
		}),
	});
}

const rowAt = (host: HTMLElement, column: number, index: number) => host.shadowRoot!.querySelectorAll("[data-content]")[column].children[index];

function selectRange(host: HTMLElement, anchorNode: Node, anchorOffset: number, focusNode: Node, focusOffset: number) {
	const selection = {
		anchorNode: anchorNode as Node | null, anchorOffset,
		focusNode: focusNode as Node | null, focusOffset,
		setBaseAndExtent: vi.fn((anchorNode: Node, anchorOffset: number, focusNode: Node, focusOffset: number): void => {
			Object.assign(selection, { anchorNode, anchorOffset, focusNode, focusOffset });
		}),
		removeAllRanges: vi.fn((): void => {
			Object.assign(selection, { anchorNode: null, anchorOffset: 0, focusNode: null, focusOffset: 0 });
		}),
	};
	Object.assign(host.shadowRoot!, { getSelection: () => selection });
	return selection;
}

function rebuild(host: HTMLElement) {
	for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
		column.replaceChildren(...[...column.children].map(child => child.cloneNode(true)));
	}
}

describe("readCodeSelection", () => {
	it("reads a unified diff's selected rows with both line numbers and their sides", () => {
		const host = surface([{ rows: [[4, "context", "const a = 1;", 4], [5, "change-deletion", "old();"], [5, "change-addition", "next();"], [8, "context", "done();", 7]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 3), 3);
		const selection = readCodeSelection(host);
		expect(selection?.rows).toEqual([
			{ kind: "context", side: "new", oldNo: 4, newNo: 4, text: "const a = 1;" },
			{ kind: "del", side: "old", oldNo: 5, newNo: null, text: "old();" },
			{ kind: "add", side: "new", oldNo: null, newNo: 5, text: "next();" },
			{ kind: "context", side: "new", oldNo: 7, newNo: 8, text: "done();" },
		]);
		expect(selection?.range).toEqual({ start: 4, side: "additions", end: 8, endSide: "additions" });
	});

	it("numbers a split diff's old-side context rows by the old file", () => {
		const host = surface([
			{ side: "deletions", rows: [[6, "context", "step();", 6], [7, "context", "done();", 8]] },
			{ side: "additions", rows: [[6, "context", "step();", 6], [8, "context", "done();", 7]] },
		]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 2);
		const selection = readCodeSelection(host);
		expect(selection?.rows).toEqual([
			{ kind: "context", side: "old", oldNo: 6, newNo: 6, text: "step();" },
			{ kind: "context", side: "old", oldNo: 7, newNo: 8, text: "done();" },
		]);
		expect(selection?.range).toEqual({ start: 6, side: "deletions", end: 7, endSide: "deletions" });
	});

 it.each(["\n", "\r\n"])("captures blank rows and whitespace across split and unified surfaces (%j)", ending => {
  for (const side of [undefined, "deletions", "additions"] as const) {
   const host = surface([{ side, rows: [[1,"context","services:",1],[2,"context",ending,2],[3,"context"," \t"+ending,3],[4,"context","  web:",4]] }]);
   select(host,rowAt(host,0,0),rowAt(host,0,3),6);
   const ref=codeReference("docker-compose.yaml",readCodeSelection(host)!,side!==undefined);
   expect(ref.lines.map(row=>row.text)).toEqual(["services:",""," \t","  web:"]);
   expect(ref.lines.map(row=>row.newNo)).toEqual([1,2,3,4]);
  }
 });

	it("leaves out a last line the selection only reaches the start of", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2], [3, "context", "three", 3]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 2), 0);
		expect(readCodeSelection(host)?.range).toMatchObject({ start: 1, end: 2 });
	});
});

describe("restoreCodeSelection", () => {
	it.each([undefined, "deletions", "additions"] as const)("restores whole-line boundaries in either direction (%s column)", side => {
		for (const backward of [false, true]) {
			const host = surface([{ side, rows: [[1, "context", "one\n", 1], [2, "context", " \t\r\n", 2], [3, "context", "three\n", 3]] }]);
			const start = rowAt(host, 0, 0).firstChild!;
			const end = rowAt(host, 0, 2).firstChild!;
			selectRange(host, backward ? end : start, 0, backward ? start : end, 0);
			const captured = readCodeSelection(host)!;
			expect(captured.range).toMatchObject({ start: 1, end: 2 });
			rebuild(host);
			expect(restoreCodeSelection(host, captured.snapshot)).toBe(true);
			const restored = readCodeSelection(host)!;
			expect(restored.textRange!.toString()).toBe("one\n \t\r\n");
			expect(codeReference("file.txt", restored, side !== undefined).lines.map(row => row.text)).toEqual(["one", " \t"]);
			host.remove();
		}
	});

	it.each([false, true])("preserves element endpoints across retokenization (backward: %s)", backward => {
		const host = surface([{ rows: [[1, "context", "", 1]] }]);
		const row = rowAt(host, 0, 0);
		row.innerHTML = "<span>α😀</span><span><b> \tβeta</b></span>";
		const start = row;
		const end = row.lastChild!.firstChild!;
		selectRange(host, backward ? end : start, 1, backward ? start : end, 1);
		const captured = readCodeSelection(host)!;
		expect(captured.textRange!.toString()).toBe(" \tβeta");
		rebuild(host);
		// Identical code, now tokenized into a different number of spans.
		rowAt(host, 0, 0).innerHTML = "<span>α</span><span>😀 \t</span><span>βeta</span>";
		expect(restoreCodeSelection(host, captured.snapshot)).toBe(true);
		expect(readCodeSelection(host)!.textRange!.toString()).toBe(" \tβeta");
	});

	it("restores a partial single-line selection at the same characters", () => {
		const host = surface([{ rows: [[1, "context", "before α😀 after\n", 1]] }]);
		const text = rowAt(host, 0, 0).firstChild!;
		selectRange(host, text, 7, text, 10);
		const captured = readCodeSelection(host)!;
		rebuild(host);
		expect(restoreCodeSelection(host, captured.snapshot)).toBe(true);
		expect(readCodeSelection(host)!.textRange!.toString()).toBe("α😀");
	});

	it("puts a selection back onto rebuilt rows at the same characters", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2], [3, "context", "three", 3]] }]);
		const placed: Array<[Node, number, Node, number]> = [];
		const recordPlacement = (base: Node, baseOffset: number, extent: Node, extentOffset: number) => { placed.push([base, baseOffset, extent, extentOffset]); };
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 2), 2, recordPlacement);
		const selection = readCodeSelection(host)!;
		expect(selection.range).toMatchObject({ start: 1, end: 3 });
		expect(selection.snapshot).toMatchObject({ anchor: { offset: 0 }, focus: { offset: 2 } });

		rebuild(host);
		expect(selection.snapshot.anchor.row.isConnected).toBe(false);
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(true);
		// The restored ends land on the new copies of the first and last rows.
		expect(placed).toEqual([
			[rowAt(host, 0, 0).firstChild, 0, rowAt(host, 0, 2).firstChild, 2],
		]);
	});

	it("leaves a live selection alone: only a rebuild restores", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 2);
		const selection = readCodeSelection(host)!;
		// Escape or a click elsewhere also collapses the selection, but its rows
		// are still connected — restoring would resurrect a cleared selection.
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(false);
	});

	it("gives up when the rebuild took the column with it", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 2);
		const selection = readCodeSelection(host)!;
		host.remove();
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(false);
	});

	it("does not move the selection onto other code when the rebuild inserted rows before it", () => {
		const host = surface([{ rows: [[1, "context", "original();", 1], [2, "context", "selected();", 2], [3, "context", "after();", 3]] }]);
		const placed: Array<[Node, number, Node, number]> = [];
		const recordPlacement = (base: Node, baseOffset: number, extent: Node, extentOffset: number) => { placed.push([base, baseOffset, extent, extentOffset]); };
		select(host, rowAt(host, 0, 1), rowAt(host, 0, 2), 5, recordPlacement);
		const selection = readCodeSelection(host)!;
		// A refresh adds a row up top: the captured indexes now name other rows.
		for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
			const inserted = column.children[0].cloneNode(true) as Element;
			inserted.setAttribute("data-line", "0");
			inserted.textContent = "inserted();";
			column.prepend(inserted);
			column.replaceChildren(...[...column.children].map((child) => child.cloneNode(true)));
		}
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(false);
		expect(placed).toEqual([]);
	});

	it("does not restore when the rebuild removed a row before the selection", () => {
		const host = surface([{ rows: [[1, "context", "first();", 1], [2, "context", "selected();", 2], [3, "context", "after();", 3]] }]);
		select(host, rowAt(host, 0, 1), rowAt(host, 0, 2), 5);
		const selection = readCodeSelection(host)!;
		for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
			const kept = [...column.children].slice(1).map((child) => child.cloneNode(true));
			column.replaceChildren(...kept);
		}
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(false);
	});

	it("does not restore when the rebuild changed a selected row's text", () => {
		const host = surface([{ rows: [[1, "context", "before();", 1], [2, "context", "selected();", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 9);
		const selection = readCodeSelection(host)!;
		for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
			column.replaceChildren(...[...column.children].map((child) => {
				const copy = child.cloneNode(true) as Element;
				if (copy.textContent === "selected();") copy.textContent = "edited();";
				return copy;
			}));
		}
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(false);
	});

	it("does not restore when the rebuild changed a row inside the selected span", () => {
		const host = surface([{ rows: [[1, "context", "alpha;", 1], [2, "context", "middle();", 2], [3, "context", "omega;", 3]] }]);
		const placed: Array<[Node, number, Node, number]> = [];
		const recordPlacement = (base: Node, baseOffset: number, extent: Node, extentOffset: number) => { placed.push([base, baseOffset, extent, extentOffset]); };
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 2), 6, recordPlacement);
		const selection = readCodeSelection(host)!;
		// Same row count, both endpoints still on their captured text: only the
		// interior row was rewritten. Restoring would highlight changed code.
		for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
			column.replaceChildren(...[...column.children].map((child) => {
				const copy = child.cloneNode(true) as Element;
				if (copy.textContent === "middle();") copy.textContent = "completely changed code";
				return copy;
			}));
		}
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(false);
		expect(placed).toEqual([]);
	});

	it("still restores when the rebuild changed rows outside the selected span", () => {
		const host = surface([{ rows: [[1, "context", "before();", 1], [2, "context", "one;", 2], [3, "context", "two;", 3], [4, "context", "after();", 4]] }]);
		const placed: Array<[Node, number, Node, number]> = [];
		const recordPlacement = (base: Node, baseOffset: number, extent: Node, extentOffset: number) => { placed.push([base, baseOffset, extent, extentOffset]); };
		select(host, rowAt(host, 0, 1), rowAt(host, 0, 2), 4, recordPlacement);
		const selection = readCodeSelection(host)!;
		// Only unselected rows were rewritten; the captured span itself is intact.
		for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
			column.replaceChildren(...[...column.children].map((child) => {
				const copy = child.cloneNode(true) as Element;
				if (copy.textContent === "before();" || copy.textContent === "after();") copy.textContent = "changed();";
				return copy;
			}));
		}
		expect(restoreCodeSelection(host, selection.snapshot)).toBe(true);
		expect(placed).toEqual([[rowAt(host, 0, 1).firstChild, 0, rowAt(host, 0, 2).firstChild, 4]]);
	});

	it.each([
		["data-line", "20"], ["data-alt-line", "21"], ["data-line-type", "change-deletion"],
	])("rejects identical interior text with changed %s", (attribute, value) => {
		for (const side of [undefined, "deletions", "additions"] as const) {
			const host = surface([{ side, rows: [[1, "context", "same();", 1], [2, "context", "same();", 2], [3, "context", "same();", 3]] }]);
			const native = selectRange(host, rowAt(host, 0, 0).firstChild!, 0, rowAt(host, 0, 2).firstChild!, 7);
			const captured = readCodeSelection(host)!;
			rebuild(host);
			rowAt(host, 0, 1).setAttribute(attribute, value);
			expect(restoreCodeSelection(host, captured.snapshot)).toBe(false);
			expect(native.setBaseAndExtent).not.toHaveBeenCalled();
			host.remove();
		}
	});

	it.each(["deletions", "additions"] as const)("rejects a rebuilt %s column that changed sides", side => {
		const host = surface([{ side, rows: [[1, "context", "same();", 1], [2, "context", "same();", 2]] }]);
		const native = selectRange(host, rowAt(host, 0, 0).firstChild!, 0, rowAt(host, 0, 1).firstChild!, 7);
		const captured = readCodeSelection(host)!;
		rebuild(host);
		const code = rowAt(host, 0, 0).parentElement!.parentElement!;
		code.removeAttribute(`data-${side}`);
		code.setAttribute(side === "deletions" ? "data-additions" : "data-deletions", "");
		expect(restoreCodeSelection(host, captured.snapshot)).toBe(false);
		expect(native.setBaseAndExtent).not.toHaveBeenCalled();
	});
});

describe("codeReference", () => {
	it("keeps an old-side diff selection on the old side, with old line numbers", () => {
		const host = surface([
			{ side: "deletions", rows: [[6, "context", "step();", 6], [7, "context", "done();", 8]] },
			{ side: "additions", rows: [[6, "context", "step();", 6], [8, "context", "done();", 7]] },
		]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 2);
		expect(codeReference("src/a.ts", readCodeSelection(host)!, true)).toMatchObject({ path: "src/a.ts", side: "old", endSide: "old", line: 6, endLine: 7 });
	});

	it("marks a file-view selection as file lines", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 3);
		expect(codeReference("README.md", readCodeSelection(host)!, false)).toMatchObject({ side: "file", line: 1, endLine: 2 });
	});
});

describe("code selection capture scheduling", () => {
	it("clears a repeated row replaced by a balanced insert/delete without attaching it", () => {
		const host = surface([{ rows: [[10, "context", "same();", 10], [11, "context", "same();", 11], [12, "context", "tail();", 12]] }]);
		const native = selectRange(host, rowAt(host, 0, 1).firstChild!, 0, rowAt(host, 0, 1).firstChild!, 7);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation(next => { callback = next; return 1; });
		const flush = () => act(() => { const next = callback; callback = undefined; next?.(0); });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const ref = { current: container };
		const deliver = vi.fn();
		const hook = renderHook(() => useCodeSelection(ref, deliver));
		flush();
		expect(hook.result.current.source.get()?.range.start).toBe(11);
		const column = rowAt(host, 0, 0).parentElement!;
		const inserted = rowAt(host, 0, 0).cloneNode(true) as Element;
		inserted.setAttribute("data-line", "9"); inserted.setAttribute("data-alt-line", "9");
		// Insert line 9 and remove the selected line 11. The same text remains at
		// index 1, but it is now line 10; total row count has not changed.
		column.replaceChildren(inserted, column.children[0].cloneNode(true), column.children[2].cloneNode(true));
		native.removeAllRanges();
		act(() => fireEvent(document, new Event("selectionchange"))); flush(); flush();
		expect(hook.result.current.source.get()).toBeNull();
		expect(native.setBaseAndExtent).not.toHaveBeenCalled();
		expect(hook.result.current.ask()).toBe(false);
		expect(deliver).not.toHaveBeenCalled();
		hook.unmount(); container.remove(); frame.mockRestore();
	});

	it.each(["dismiss", "ask", "reject", "click-away"] as const)("keeps %s authoritative across an immediate redraw", action => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		const native = selectRange(host, rowAt(host, 0, 0).firstChild!, 0, rowAt(host, 0, 1).firstChild!, 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation(next => { callback = next; return 1; });
		const flush = () => act(() => { const next = callback; callback = undefined; next?.(0); });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const ref = { current: container };
		const deliver = vi.fn(() => action === "reject" ? false : undefined);
		const hook = renderHook(() => useCodeSelection(ref, deliver));
		flush();
		expect(hook.result.current.source.get()).not.toBeNull();
		act(() => {
			if (action === "dismiss") { hook.result.current.dismiss(); hook.result.current.dismiss(); }
			else if (action === "click-away") {
				fireEvent(document.body, new MouseEvent("pointerdown", { button: 0, bubbles: true }));
				native.removeAllRanges();
			}
			else expect(hook.result.current.ask()).toBe(action !== "reject");
		});
		// Redraw before the click-away selectionchange has been processed too.
		rebuild(host);
		act(() => fireEvent(document, new Event("selectionchange")));
		flush(); flush();
		if (action === "reject") {
			expect(native.setBaseAndExtent).toHaveBeenCalledOnce();
			expect(hook.result.current.source.get()?.rows.map(row => row.text)).toEqual(["one", "two"]);
			deliver.mockReturnValue(undefined);
			act(() => { expect(hook.result.current.ask()).toBe(true); });
			expect(deliver).toHaveBeenCalledTimes(2);
		} else {
			expect(native.setBaseAndExtent).not.toHaveBeenCalled();
			expect(hook.result.current.source.get()).toBeNull();
		}
		hook.unmount(); container.remove(); frame.mockRestore();
	});

	it.each(["host", "toolbar", "context-menu"] as const)("retains the selection during a %s pointerdown and immediate redraw", target => {
		const host = surface([{ rows: [[1, "context", "one", 1]] }]);
		const native = selectRange(host, rowAt(host, 0, 0).firstChild!, 0, rowAt(host, 0, 0).firstChild!, 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation(next => { callback = next; return 1; });
		const flush = () => act(() => { const next = callback; callback = undefined; next?.(0); });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const toolbar = document.createElement("button"); toolbar.setAttribute("data-selection-ask", "");
		const icon = document.createElement("span"); toolbar.append(icon); document.body.append(toolbar);
		const ref = { current: container };
		const hook = renderHook(() => useCodeSelection(ref, vi.fn()));
		flush();
		act(() => {
			const element = target === "host" ? rowAt(host, 0, 0) : target === "toolbar" ? icon : document.body;
			fireEvent(element, new MouseEvent("pointerdown", { button: target === "context-menu" ? 2 : 0, bubbles: true, composed: true }));
		});
		expect(hook.result.current.source.get()).not.toBeNull();
		rebuild(host); native.removeAllRanges();
		act(() => fireEvent(document, new Event("selectionchange")));
		flush(); flush();
		expect(native.setBaseAndExtent).toHaveBeenCalledOnce();
		expect(hook.result.current.source.get()?.rows.map(row => row.text)).toEqual(["one"]);
		hook.unmount(); container.remove(); toolbar.remove(); frame.mockRestore();
	});

	it("attempts a refused native restore only once", () => {
		const host = surface([{ rows: [[1, "context", "one", 1]] }]);
		const native = selectRange(host, rowAt(host, 0, 0).firstChild!, 0, rowAt(host, 0, 0).firstChild!, 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation(next => { callback = next; return 1; });
		const flush = () => act(() => { const next = callback; callback = undefined; next?.(0); });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const ref = { current: container };
		const hook = renderHook(() => useCodeSelection(ref, vi.fn()));
		flush();
		rebuild(host); native.removeAllRanges(); native.setBaseAndExtent.mockImplementation(() => {});
		act(() => fireEvent(document, new Event("selectionchange")));
		flush(); flush();
		expect(native.setBaseAndExtent).toHaveBeenCalledOnce();
		expect(hook.result.current.source.get()).toBeNull();
		expect(callback).toBeUndefined();
		hook.unmount(); container.remove(); frame.mockRestore();
	});

	it.each(["disable", "unmount"])("cancels pending restoration on %s", action => {
		const host = surface([{ rows: [[1, "context", "one", 1]] }]);
		const native = selectRange(host, rowAt(host, 0, 0).firstChild!, 0, rowAt(host, 0, 0).firstChild!, 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation(next => { callback = next; return 1; });
		const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => { callback = undefined; });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const ref = { current: container };
		const hook = renderHook(({ enabled }) => useCodeSelection(ref, vi.fn(), enabled), { initialProps: { enabled: true } });
		act(() => { const next = callback; callback = undefined; next?.(0); });
		rebuild(host); native.removeAllRanges();
		act(() => fireEvent(document, new Event("selectionchange")));
		expect(callback).toBeDefined();
		const source = hook.result.current.source;
		if (action === "disable") hook.rerender({ enabled: false });
		else hook.unmount();
		expect(callback).toBeUndefined();
		expect(cancel).toHaveBeenCalledWith(1);
		expect(source.get()).toBeNull();
		expect(native.setBaseAndExtent).not.toHaveBeenCalled();
		if (action === "disable") hook.unmount();
		container.remove(); frame.mockRestore(); cancel.mockRestore();
	});

	it("exposes an explicit toolbar dismissal without handling other surfaces' Escape", () => {
		const host = surface([{ rows: [[1, "context", "one", 1]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 0), 3);
		const native = (host.shadowRoot as ShadowRoot & { getSelection: () => Selection }).getSelection();
		Object.assign(host.shadowRoot!, { getSelection: () => native });
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation(next => { callback = next; return 1; });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const hook = renderHook(() => useCodeSelection({ current: container }, vi.fn()));
		act(() => callback?.(0));
		const input = document.createElement("input"); document.body.append(input);
		expect(fireEvent.keyDown(input, { key: "Escape" })).toBe(true);
		expect(hook.result.current.source.get()).not.toBeNull();
		act(() => hook.result.current.dismiss());
		expect(native.removeAllRanges).toHaveBeenCalledOnce();
		expect(hook.result.current.source.get()).toBeNull();
		hook.unmount(); container.remove(); input.remove(); frame.mockRestore();
	});
	it("does not read rows or notify subscribers during sustained scrolling", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation((next) => { callback = next; return 1; });
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const containerRef = { current: container };
		const deliver = vi.fn();
		const hook = renderHook(() => useCodeSelection(containerRef, deliver));
		act(() => { const next = callback; callback = undefined; next?.(0); });
		const captured = hook.result.current.source.get();
		expect(captured?.rows).toHaveLength(2);
		const listener = vi.fn();
		const unsubscribe = hook.result.current.source.subscribe(listener);
		const readRows = vi.spyOn(host.shadowRoot!, "querySelectorAll");
		frame.mockClear();
		for (let i = 0; i < 100; i++) fireEvent.scroll(document);
		expect(readRows).not.toHaveBeenCalled();
		expect(frame).not.toHaveBeenCalled();
		expect(listener).not.toHaveBeenCalled();
		expect(hook.result.current.source.get()).toBe(captured);
		const originalSelection = (host.shadowRoot as ShadowRoot & { getSelection: () => unknown }).getSelection;
		Object.assign(host.shadowRoot!, { getSelection: () => null });
		expect(hook.result.current.ask()).toBe(false);
		expect(deliver).not.toHaveBeenCalled();
		Object.assign(host.shadowRoot!, { getSelection: originalSelection });
		act(() => {
			expect(hook.result.current.ask()).toBe(true);
			expect(hook.result.current.ask()).toBe(false);
		});
		expect(deliver).toHaveBeenCalledExactlyOnceWith(captured);
		unsubscribe(); hook.unmount(); container.remove(); frame.mockRestore();
	});
	it("refuses a detached capture before its observer callback runs", () => {
		const host = surface([{ rows: [[1, "context", "one", 1]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 0), 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation((next) => { callback = next; return 1; });
		const ask = vi.fn();
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const containerRef = { current: container };
		const hook = renderHook(() => useCodeSelection(containerRef, ask));
		act(() => callback?.(0));
		host.remove();
		expect(hook.result.current.ask()).toBe(false);
		expect(ask).not.toHaveBeenCalled();
		hook.unmount(); container.remove(); frame.mockRestore();
	});
	it("republishes the fresh range after an identical-row rebuild so the toolbar can act", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 3);
		let callback: FrameRequestCallback | undefined;
		const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation((next) => { callback = next; return 1; });
		const deliver = vi.fn();
		const container = document.createElement("div"); container.append(host); document.body.append(container);
		const containerRef = { current: container };
		const hook = renderHook(() => useCodeSelection(containerRef, deliver));
		act(() => { const next = callback; callback = undefined; next?.(0); });
		const captured = hook.result.current.source.get()!;
		// The viewer re-tokenizes: every row is swapped for a fresh identical copy,
		// and the browser's selection lands on the replacements.
		for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
			column.replaceChildren(...[...column.children].map((child) => child.cloneNode(true)));
		}
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 3);
		// The redraw (observed) or the restored selection (selectionchange) schedules
		// the next capture; run one frame and read what it publishes.
		act(() => { document.dispatchEvent(new Event("selectionchange")); const next = callback; callback = undefined; next?.(0); });
		const fresh = hook.result.current.source.get();
		// Same lines and geometry, but the store must swap to the fresh object:
		// its old Range collapsed onto the removed rows, and a stale textRange
		// makes the toolbar refuse Cmd/Ctrl+L.
		expect(fresh).not.toBe(captured);
		expect(fresh!.textRange!.startContainer).toBe(rowAt(host, 0, 0).firstChild);
		expect(fresh!.textRange!.startContainer.isConnected).toBe(true);
		expect(fresh!.snapshot.rows.map(row => row.text)).toEqual(["one", "two"]);
		expect(hook.result.current.ask()).toBe(true);
		expect(deliver).toHaveBeenCalledExactlyOnceWith(fresh);
		hook.unmount(); container.remove(); frame.mockRestore();
	});
});
