import { describe, expect, it } from "vitest";
import { csvPreviewRows, tabularDelimiter } from "./csv-preview";

describe("csv preview", () => {
	it("picks a delimiter from the file extension", () => {
		expect(tabularDelimiter("data/sizes.CSV")).toBe(",");
		expect(tabularDelimiter("data/sizes.tsv")).toBe("\t");
		expect(tabularDelimiter("plan.md")).toBeUndefined();
	});

	it("parses quoted fields, doubled quotes, embedded line breaks, CRLF, and a BOM", () => {
		const csv = "﻿name,note\r\n\"Smith, J\",\"said \"\"hi\"\"\"\r\n\r\nplain,\"two\nlines\"\r\n";
		expect(csvPreviewRows(csv, ",", 10, 10)).toEqual([
			["name", "note"],
			["Smith, J", "said \"hi\""],
			["plain", "two\nlines"],
		]);
	});

	it("keeps only the leading rows and columns", () => {
		const csv = "a,b,c\n1,2,3\n4,5,6\n7,8,9";
		expect(csvPreviewRows(csv, ",", 2, 2)).toEqual([["a", "b"], ["1", "2"]]);
	});

	it("splits tab-separated text", () => {
		expect(csvPreviewRows("a\tb\n1\t2", "\t", 10, 10)).toEqual([["a", "b"], ["1", "2"]]);
	});
});
