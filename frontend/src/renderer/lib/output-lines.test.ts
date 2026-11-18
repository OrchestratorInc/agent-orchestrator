import { describe, expect, it } from "vitest";
import { lineTone, outputLines, outputSummary } from "./output-lines";

describe("output lines", () => {
	it.each([
		["src/ui/Stage.tsx:48:7 - error TS2322: Type 'string' is not assignable", "error"],
		["TypeError: guests.map is not a function", "error"],
		["npm ERR! code ELIFECYCLE", "error"],
		["ERROR in ./src/index.ts", "error"],
		["× src/ui/GuestList.test.tsx > empty state", "error"],
		["⚠ Fast Refresh had to perform a full reload", "warning"],
		["Warning: Each child in a list should have a unique key", "warning"],
		["npm WARN deprecated inflight@1.0.6", "warning"],
		["✓ Compiled in 212ms", "ok"],
		["Found 1 error.", undefined],
		["Test Files  4 passed (4), 0 errors", undefined],
		["GET /api/errors 200 in 41ms", undefined],
		["", undefined],
	])("reads %j as %s", (line, tone) => {
		expect(lineTone(line)).toBe(tone);
	});

	it("splits lines without inventing one after the final newline", () => {
		expect(outputLines("")).toEqual([]);
		expect(outputLines("a\n\nb\n")).toEqual(["a", "", "b"]);
		expect(outputLines("a\nb")).toEqual(["a", "b"]);
	});

	it("counts lines, warnings and errors", () => {
		expect(outputSummary("✓ ok\nnpm WARN old\nerror: bad\nerror: worse\n")).toEqual({ lines: 4, warnings: 1, errors: 2 });
	});
});
