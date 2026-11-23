import { describe, expect, it, vi } from "vitest";
import { fallbackCharacters, textEdits, type TextEdit } from "./textDiff";

// Plays edits into a field whose caret starts at the end, the way the page's
// field sees them, and returns the text and whether the caret is back at the end.
function replay(start: string, edits: TextEdit[]): { text: string; caretAtEnd: boolean } {
	const chars = Array.from(start);
	let caret = chars.length;
	for (const edit of edits) {
		if (edit.kind === "text") {
			const inserted = Array.from(edit.value);
			chars.splice(caret, 0, ...inserted);
			caret += inserted.length;
		} else if (edit.key === "Backspace") {
			if (caret > 0) chars.splice(--caret, 1);
		} else if (edit.key === "ArrowLeft") {
			caret = Math.max(0, caret - 1);
		} else {
			caret = Math.min(chars.length, caret + 1);
		}
	}
	return { text: chars.join(""), caretAtEnd: caret === chars.length };
}

const backspace = { kind: "key", key: "Backspace", code: "Backspace" } as const;

describe("textEdits", () => {
	it("types an insertion at the end as text", () => {
		expect(textEdits("hel", "hello")).toEqual([{ kind: "text", value: "lo" }]);
	});

	it("deletes from the end with Backspace", () => {
		expect(textEdits("hello", "he")).toEqual([backspace, backspace, backspace]);
	});

	it("sends nothing when the text is unchanged", () => {
		expect(textEdits("same", "same")).toEqual([]);
	});

	it("replaces in the middle by moving the caret there and back", () => {
		const edits = textEdits("the cat sat", "the dog sat");
		expect(edits.filter((edit) => edit.kind === "key" && edit.key === "ArrowLeft")).toHaveLength(4);
		expect(edits.filter((edit) => edit.kind === "key" && edit.key === "Backspace")).toHaveLength(3);
		expect(edits).toContainEqual({ kind: "text", value: "dog" });
		expect(replay("the cat sat", edits)).toEqual({ text: "the dog sat", caretAtEnd: true });
	});

	it("never splits a surrogate pair", () => {
		expect(textEdits("hi 👍", "hi ")).toEqual([backspace]);
		expect(textEdits("hi ", "hi 👍🏽")).toEqual([{ kind: "text", value: "👍🏽" }]);
		// Two emoji sharing a high surrogate differ only in the low one.
		const edits = textEdits("😀", "😁");
		expect(edits).toEqual([backspace, { kind: "text", value: "😁" }]);
		for (const edit of edits) if (edit.kind === "text") expect(edit.value).not.toMatch(/^[\uDC00-\uDFFF]/);
	});

	it("follows an IME composition step by step", () => {
		const steps = ["", "k", "か", "かn", "かな", "仮名", "仮名を"];
		let page = "";
		for (let i = 1; i < steps.length; i++) {
			const result = replay(page, textEdits(steps[i - 1]!, steps[i]!));
			expect(result).toEqual({ text: steps[i], caretAtEnd: true });
			page = result.text;
		}
		expect(textEdits("かな", "仮名")).toEqual([backspace, backspace, { kind: "text", value: "仮名" }]);
	});

	it("replays autocorrect of the last word exactly", () => {
		const edits = textEdits("i liek", "I like ");
		expect(replay("i liek", edits)).toEqual({ text: "I like ", caretAtEnd: true });
	});
});

// Engines without Intl.Segmenter (Hermes may lack it) split by this instead,
// and must still count a joined emoji as the one character the page deletes.
describe("fallbackCharacters", () => {
	it("keeps each emoji sequence and accented letter whole", () => {
		expect(fallbackCharacters("a👨‍👩‍👧‍👦b")).toEqual(["a", "👨‍👩‍👧‍👦", "b"]);
		expect(fallbackCharacters("👍🏽!")).toEqual(["👍🏽", "!"]);
		expect(fallbackCharacters("🇮🇳🇯🇵")).toEqual(["🇮🇳", "🇯🇵"]);
		expect(fallbackCharacters("1️⃣x")).toEqual(["1️⃣", "x"]);
		expect(fallbackCharacters("e\u0301t")).toEqual(["e\u0301", "t"]);
		expect(fallbackCharacters("❤️")).toEqual(["❤️"]);
		// A ZWJ joins two pictographs, never a letter after it (GB11).
		expect(fallbackCharacters("a\u200db")).toEqual(["a\u200d", "b"]);
		expect(fallbackCharacters("👍\u200db")).toEqual(["👍\u200d", "b"]);
	});

	it("agrees with Intl.Segmenter on these", () => {
		const segmenter = new Intl.Segmenter(undefined, { granularity: "grapheme" });
		for (const text of ["a👨‍👩‍👧‍👦b", "👍🏽!", "🇮🇳🇯🇵", "1️⃣x", "e\u0301t", "plain text", "🏳️‍🌈", "a\u200db", "👍\u200db", "❤️‍🔥"]) {
			expect(fallbackCharacters(text)).toEqual(Array.from(segmenter.segment(text), (part) => part.segment));
		}
	});
});

describe("textEdits without Intl.Segmenter", () => {
	it("deletes a joined emoji with one Backspace", async () => {
		vi.resetModules();
		vi.stubGlobal("Intl", { ...Intl, Segmenter: undefined });
		try {
			delete (globalThis.Intl as { Segmenter?: unknown }).Segmenter;
			const { textEdits: fallbackEdits } = await import("./textDiff");
			expect(fallbackEdits("hi 👨‍👩‍👧‍👦", "hi ")).toEqual([backspace]);
			expect(fallbackEdits("a\u200db", "")).toEqual([backspace, backspace]);
		} finally {
			vi.unstubAllGlobals();
			vi.resetModules();
		}
	});
});
