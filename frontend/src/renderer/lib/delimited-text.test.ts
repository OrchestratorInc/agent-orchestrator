import { describe, expect, it } from "vitest";
import { isDelimitedTextPath, parseDelimitedText } from "./delimited-text";

describe("parseDelimitedText", () => {
	it("splits CSV rows and ignores a trailing newline", () => {
		expect(parseDelimitedText("name,count\nalpha,1\nbeta,2\n", "data.csv")).toEqual([["name", "count"], ["alpha", "1"], ["beta", "2"]]);
	});

	it("keeps delimiters, newlines and escaped quotes inside quoted fields", () => {
		expect(parseDelimitedText("a,b\r\n\"x, y\",\"line 1\nline 2\"\r\n\"say \"\"hi\"\"\",", "data.csv")).toEqual([
			["a", "b"],
			["x, y", "line 1\nline 2"],
			["say \"hi\"", ""],
		]);
	});

	it("opens a quoted field after leading whitespace", () => {
		expect(parseDelimitedText("a, \"b,c\"\n", "data.csv")).toEqual([["a", "b,c"]]);
	});

	it("splits a TSV on tabs and drops a byte order mark", () => {
		expect(parseDelimitedText("﻿name\tnote\nalpha\thas, comma", "data.TSV")).toEqual([["name", "note"], ["alpha", "has, comma"]]);
	});

	it("returns no rows for an empty file", () => {
		expect(parseDelimitedText("", "empty.csv")).toEqual([]);
	});

	it("recognises CSV and TSV paths only", () => {
		expect(["a.csv", "b.TSV"].map(isDelimitedTextPath)).toEqual([true, true]);
		expect(["a.txt", "csv", "a.csv.bak"].map(isDelimitedTextPath)).toEqual([false, false, false]);
	});
});
