import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { droppedPathsText, posixShellQuote } from "./terminal-drop";

// File names a person may drop, each carrying something a shell would act on.
const hostileNames = [
	"it's a test.txt",
	'say "hi".txt',
	"a$(id)b.txt",
	"semi;`id`.txt",
	"x & y.txt",
	"x | y.txt",
	"plain name with  spaces.txt",
	"'$(touch pwned)'.txt",
	"tail\\'.txt",
];

describe("posixShellQuote", () => {
	it("wraps every value in single quotes and writes an embedded quote as '\\''", () => {
		expect(posixShellQuote("/workspace/.ao/attachments/notes.txt")).toBe("'/workspace/.ao/attachments/notes.txt'");
		expect(posixShellQuote("it's")).toBe("'it'\\''s'");
		expect(posixShellQuote("a$(id)b")).toBe("'a$(id)b'");
		expect(posixShellQuote("semi;`id`")).toBe("'semi;`id`'");
	});

	it.skipIf(process.platform === "win32")("reaches a POSIX shell as exactly the name, with nothing run", () => {
		for (const name of hostileNames) {
			const path = `/workspace/.ao/attachments/${name}`;
			const echoed = execFileSync("/bin/sh", ["-c", `printf '%s' ${posixShellQuote(path)}`], { encoding: "utf8" });
			expect(echoed).toBe(path);
		}
	});
});

describe("droppedPathsText", () => {
	it("always quotes sandbox paths, one argument each", () => {
		const paths = hostileNames.map((name) => `/workspace/.ao/attachments/${name}`);
		const text = droppedPathsText(paths, true);
		expect(text.endsWith(" ")).toBe(true);
		expect(text).toBe(`${paths.map(posixShellQuote).join(" ")} `);
		expect(droppedPathsText(["/workspace/a.txt"], true)).toBe("'/workspace/a.txt' ");
	});

	it.skipIf(process.platform === "win32")("types sandbox paths a POSIX shell splits into the same arguments", () => {
		const paths = hostileNames.map((name) => `/workspace/.ao/attachments/${name}`);
		const echoed = execFileSync("/bin/sh", ["-c", `printf '%s\\n' ${droppedPathsText(paths, true)}`], { encoding: "utf8" });
		expect(echoed).toBe(paths.map((path) => `${path}\n`).join(""));
	});

	it("keeps a local path as before: bare, or quoted around whitespace", () => {
		expect(droppedPathsText(["/tmp/ao-dropped/notes.txt"], false)).toBe("/tmp/ao-dropped/notes.txt ");
		expect(droppedPathsText(["/Users/a b/.ao/electron/terminal-drops/1-notes.txt"], false)).toBe("'/Users/a b/.ao/electron/terminal-drops/1-notes.txt' ");
	});
});
