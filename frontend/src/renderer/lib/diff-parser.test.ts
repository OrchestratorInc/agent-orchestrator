import { describe, expect, it } from "vitest";
import { parseUnifiedDiff } from "./diff-parser";

const rows = (raw: string) => parseUnifiedDiff(raw).filter((row) => row.kind !== "hunk").map((row) => [row.kind, row.text]);

describe("parseUnifiedDiff", () => {
	it("keeps a counted hunk's lines that start like a file header", () => {
		// A removed `-- comment` reads `--- comment`; an added `++ counter` reads `+++ counter`.
		const raw = [
			"diff --git a/q.sql b/q.sql",
			"index 1111111..2222222 100644",
			"--- a/q.sql",
			"+++ b/q.sql",
			"@@ -1,3 +1,3 @@",
			" select 1;",
			"--- old note",
			"+++ counter",
			" select 2;",
		].join("\n");
		expect(rows(raw)).toEqual([
			["context", "select 1;"],
			["del", "-- old note"],
			["add", "++ counter"],
			["context", "select 2;"],
		]);
	});

	it("keeps a header-like line in a hunk that states no size", () => {
		// An editor's old/new text arrives with no ranges; the card gives it a bare header.
		expect(rows("@@ -1 +1 @@\n--- a comment\n+select 1;\n index stays")).toEqual([
			["del", "-- a comment"],
			["add", "select 1;"],
			["context", "index stays"],
		]);
	});

	it("still hides the next file's header in a diff of several files", () => {
		const raw = [
			"diff --git a/a.ts b/a.ts",
			"--- a/a.ts",
			"+++ b/a.ts",
			"@@ -1 +1 @@",
			"-a",
			"+b",
			"diff --git a/c.ts b/c.ts",
			"index 3333333..4444444 100644",
			"--- a/c.ts",
			"+++ b/c.ts",
			"@@ -1,2 +1,2 @@",
			" keep",
			"-c",
			"+d",
		].join("\n");
		expect(rows(raw)).toEqual([
			["del", "a"],
			["add", "b"],
			["context", "keep"],
			["del", "c"],
			["add", "d"],
		]);
		expect(parseUnifiedDiff(raw).filter((row) => row.kind === "hunk")).toHaveLength(2);
	});
});
