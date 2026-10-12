import { describe, expect, it } from "vitest";
import { codeReference, readCodeSelection } from "./useCodeSelection";

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
function select(host: HTMLElement, from: Element, to: Element, endOffset: number) {
	const hostRange = document.createRange();
	hostRange.selectNode(host);
	Object.assign(host.shadowRoot!, {
		getSelection: () => ({
			isCollapsed: true,
			rangeCount: 1,
			anchorNode: from.firstChild,
			anchorOffset: 0,
			focusNode: to.firstChild,
			focusOffset: endOffset,
			toString: () => "",
			getRangeAt: () => hostRange,
		}),
	});
}

const rowAt = (host: HTMLElement, column: number, index: number) => host.shadowRoot!.querySelectorAll("[data-content]")[column].children[index];

describe("readCodeSelection", () => {
	it("reads a unified diff's selected rows with both line numbers and their sides", () => {
		const host = surface([{ rows: [[4, "context", "const a = 1;", 4], [5, "change-deletion", "old();"], [5, "change-addition", "next();"], [8, "context", "done();", 7]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 3), 3);
		const selection = readCodeSelection(host);
		expect(selection?.rows).toEqual([
			{ kind: "context", oldNo: 4, newNo: 4, text: "const a = 1;" },
			{ kind: "del", oldNo: 5, newNo: null, text: "old();" },
			{ kind: "add", oldNo: null, newNo: 5, text: "next();" },
			{ kind: "context", oldNo: 7, newNo: 8, text: "done();" },
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
			{ kind: "context", oldNo: 6, newNo: 6, text: "step();" },
			{ kind: "context", oldNo: 7, newNo: 8, text: "done();" },
		]);
		expect(selection?.range).toEqual({ start: 6, side: "deletions", end: 7, endSide: "deletions" });
	});

	it("leaves out a last line the selection only reaches the start of", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2], [3, "context", "three", 3]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 2), 0);
		expect(readCodeSelection(host)?.range).toMatchObject({ start: 1, end: 2 });
	});
});

describe("codeReference", () => {
	it("keeps an old-side diff selection on the old side, with old line numbers", () => {
		const host = surface([
			{ side: "deletions", rows: [[6, "context", "step();", 6], [7, "context", "done();", 8]] },
			{ side: "additions", rows: [[6, "context", "step();", 6], [8, "context", "done();", 7]] },
		]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 2);
		expect(codeReference("src/a.ts", readCodeSelection(host)!, true)).toMatchObject({ path: "src/a.ts", side: "old", line: 6, endLine: 7 });
	});

	it("marks a file-view selection as file lines", () => {
		const host = surface([{ rows: [[1, "context", "one", 1], [2, "context", "two", 2]] }]);
		select(host, rowAt(host, 0, 0), rowAt(host, 0, 1), 3);
		expect(codeReference("README.md", readCodeSelection(host)!, false)).toMatchObject({ side: "file", line: 1, endLine: 2 });
	});
});
