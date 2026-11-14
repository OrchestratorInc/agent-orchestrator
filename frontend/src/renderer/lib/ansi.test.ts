import { describe, expect, it } from "vitest";
import { caretNotation, stripAnsi } from "./ansi";

const ESC = "\u001b";

describe("stripAnsi", () => {
	it("returns text with nothing to strip unchanged, by identity", () => {
		const plain = "ok  github.com/aoagents/ao/internal/domain\t0.412s\n";
		expect(stripAnsi(plain)).toBe(plain);
	});

	it("removes SGR colour runs", () => {
		expect(stripAnsi(`${ESC}[32mok${ESC}[0m  pkg/a`)).toBe("ok  pkg/a");
	});

	it("removes a CSI sequence whose final byte is @", () => {
		expect(stripAnsi(`${ESC}[@${ESC}[2K${ESC}[?25lok`)).toBe("ok");
	});

	it("leaves text that follows a complete CSI sequence, as a terminal would", () => {
		expect(stripAnsi(`${ESC}[@p${ESC}[@r`)).toBe("pr");
	});

	it("removes an OSC sequence terminated by BEL", () => {
		expect(stripAnsi(`${ESC}]0;window title\u0007done`)).toBe("done");
	});

	it("removes an OSC sequence terminated by ST", () => {
		expect(stripAnsi(`${ESC}]8;;https://example.com${ESC}\\link`)).toBe("link");
	});

	it("removes a single-character escape", () => {
		expect(stripAnsi(`a${ESC}Mb`)).toBe("ab");
	});

	it("removes a charset selection escape", () => {
		expect(stripAnsi(`${ESC}(Bplain`)).toBe("plain");
	});

	it("collapses a carriage-return progress bar to the frame the user would have seen", () => {
		expect(stripAnsi("downloading  12%\rdownloading  57%\rdownloading 100%")).toBe(
			"downloading 100%",
		);
	});

	it("overwrites in place rather than clearing, which is what a terminal does", () => {
		expect(stripAnsi("abcdef\rxy")).toBe("xycdef");
	});

	it("applies backspace as a cursor move", () => {
		expect(stripAnsi("abc\b\bX")).toBe("aXc");
	});

	it("keeps carriage-return handling per line", () => {
		expect(stripAnsi("one\rtwo\nfour\rfive")).toBe("two\nfive");
	});

	it("drops leftover control characters but keeps tabs and newlines", () => {
		expect(stripAnsi("a\u0007b\u0000c\td\ne")).toBe("abc\td\ne");
	});

	it("handles the empty string", () => {
		expect(stripAnsi("")).toBe("");
	});

	it("does not leak a sequence cut mid-stream", () => {
		expect(stripAnsi(`ok${ESC}`)).toBe("ok");
		expect(stripAnsi(`ok${ESC}[`)).toBe("ok");
		expect(stripAnsi(`ok${ESC}[3`)).toBe("ok");
	});
});

describe("caretNotation", () => {
	it("spells an abort the way a terminal spells it", () => {
		expect(caretNotation("\u0003")).toBe("^C");
	});

	it("spells a carriage return as ^M", () => {
		expect(caretNotation("y\r")).toBe("y^M");
	});

	it("spells escape as ^[", () => {
		expect(caretNotation(`${ESC}[A`)).toBe("^[[A");
	});

	it("keeps newlines and tabs literal, so a paste reads as lines", () => {
		expect(caretNotation("one\ntwo\tthree")).toBe("one\ntwo\tthree");
	});

	it("spells delete as ^?", () => {
		expect(caretNotation("\u007f")).toBe("^?");
	});

	it("leaves printable text alone", () => {
		expect(caretNotation("git status")).toBe("git status");
	});
});
