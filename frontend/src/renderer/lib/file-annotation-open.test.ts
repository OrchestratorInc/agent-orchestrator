import { describe, expect, it } from "vitest";
import type { ChatFileReference } from "../../shared/chat-file-reference";
import { fileAnnotationRange } from "./file-annotation-open";

const reference = {
	path: "a.ts", side: "new", line: 2, endLine: 3,
	lines: [{ kind: "add", oldNo: null, newNo: 2, text: "b" }, { kind: "add", oldNo: null, newNo: 3, text: "c" }],
} as unknown as ChatFileReference;

describe("fileAnnotationRange", () => {
	it("keeps the captured length from a moved start that names no end", () => {
		const range = fileAnnotationRange(reference, { state: "moved", line: 4, newLineOffset: 2 });
		expect(range).toMatchObject({ line: 4, endLine: 5 });
		expect(range?.lines.map((row) => row.newNo)).toEqual([4, 5]);
	});

	it("uses the resolution's own end, and the captured range when nothing moved", () => {
		expect(fileAnnotationRange(reference, { state: "moved", line: 4, endLine: 7 })).toMatchObject({ line: 4, endLine: 7 });
		expect(fileAnnotationRange(reference, { state: "unchanged" })).toMatchObject({ line: 2, endLine: 3 });
	});
});
