import { describe, expect, it } from "vitest";
import { textEdits, type TextEdit } from "./textDiff";

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
