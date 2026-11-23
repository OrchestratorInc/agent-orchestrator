import { describe, expect, it } from "vitest";
import type { BrowserCommand, BrowserControl, FocusedEditable } from "../browserTransport";
import { canType, keyboardTraits, PageTyping, TEXT_CHUNK } from "./pageTyping";

function typing() {
	const sent: BrowserCommand[] = [];
	const page = new PageTyping((command) => sent.push(command));
	const typed = () => sent.flatMap((command) => {
		const payload = command.type === "input" ? command.payload as { kind: string; value?: string; key?: string; phase?: string } : null;
		if (payload?.kind === "text") return [payload.value!];
		if (payload?.kind === "key" && payload.phase === "down") return [`<${payload.key}>`];
		return [];
	}).join("");
	return { page, sent, typed };
}

const text = (value?: string): FocusedEditable => ({ kind: "text", value });

describe("keyboard traits", () => {
	it("picks the keyboard for the field's kind", () => {
		expect(keyboardTraits("email").keyboardType).toBe("email-address");
		expect(keyboardTraits("number").keyboardType).toBe("decimal-pad");
		expect(keyboardTraits("tel").keyboardType).toBe("phone-pad");
		expect(keyboardTraits("url").keyboardType).toBe("url");
		expect(keyboardTraits("search").returnKeyType).toBe("search");
		expect(keyboardTraits("textarea").multiline).toBe(true);
		expect(keyboardTraits("text").secureTextEntry).toBe(false);
	});

	it("opens only for a focused field the person controls", () => {
		const cases: Array<[FocusedEditable, BrowserControl, boolean]> = [
			[text(""), "human", true],
			[text(""), "agent", false],
			[text(""), "none", false],
			[null, "human", false],
		];
		for (const [focused, control, expected] of cases) expect(canType(focused, control)).toBe(expected);
	});
});

describe("PageTyping", () => {
	it("types the difference between keystrokes", () => {
		const { page, typed } = typing();
		expect(page.observe(text("hi"), 1, "human")).toBe("hi");
		page.change("hi!");
		page.change("hi");
		expect(typed()).toBe("!<Backspace>");
	});

	it("textDiff resets after reconnect", () => {
		const { page, sent, typed } = typing();
		page.observe(text(""), 1, "human");
		page.change("abc");
		expect(typed()).toBe("abc");

		// The stream drops: keystrokes meanwhile go nowhere and the field reverts.
		page.disconnect();
		expect(page.change("abcd")).toBe("abc");
		expect(typed()).toBe("abc");

		// The new stream reports the page's value, which becomes the baseline.
		expect(page.observe(text("abc"), 2, "human")).toBe("abc");
		page.change("abcd");
		expect(typed()).toBe("abcd");
		// Nothing duplicated, nothing lost.
		expect(sent).toEqual([
			{ type: "input", payload: { kind: "text", value: "abc" } },
			{ type: "input", payload: { kind: "text", value: "d" } },
		]);
	});

	it("keeps the phone's text when the page echoes a value mid-typing", () => {
		const { page, typed } = typing();
		page.observe(text(""), 1, "human");
		page.change("ab");
		// The page reports "a" while "b" is still on its way.
		expect(page.observe(text("a"), 1, "human")).toBeNull();
		page.change("abc");
		expect(typed()).toBe("abc");
	});

	it("text dropped when control is agent", () => {
		const { page, sent } = typing();
		page.observe(text("q"), 1, "human");
		page.observe(text("q"), 1, "agent");
		// The agent may have focused another field: nothing typed reaches it.
		expect(page.change("qx")).toBe("q");
		page.key("Enter");
		expect(sent).toEqual([]);

		// Back with the person, typing starts again from the page's own value.
		expect(page.observe(text("agent wrote"), 1, "human")).toBe("agent wrote");
		page.change("agent wrote!");
		expect(sent).toEqual([{ type: "input", payload: { kind: "text", value: "!" } }]);
	});

	it("starts again from the page's value when a tap moves focus to another field", () => {
		const { page, typed } = typing();
		page.observe(text(""), 1, "human");
		page.change("first");
		page.tapped();
		expect(page.observe(text("second"), 1, "human")).toBe("second");
		page.change("second!");
		expect(typed()).toBe("first!");
	});

	it("resets when the field's kind changes", () => {
		const { page } = typing();
		page.observe(text("a"), 1, "human");
		expect(page.observe({ kind: "password" }, 1, "human")).toBe("");
		expect(page.observe(null, 1, "human")).toBe("");
	});

	it("turns a newline into Enter", () => {
		const { page, typed } = typing();
		page.observe({ kind: "textarea", value: "" }, 1, "human");
		page.change("one\ntwo");
		expect(typed()).toBe("one<Enter>two");
	});

	it("splits a long paste into chunks the desktop accepts", () => {
		const { page, sent } = typing();
		page.observe(text(""), 1, "human");
		page.change("x".repeat(TEXT_CHUNK * 2 + 5));
		const lengths = sent.map((command) => ((command.payload as { value: string }).value).length);
		expect(lengths).toEqual([TEXT_CHUNK, TEXT_CHUNK, 5]);
	});

	it("sends a key down and up for Enter, Tab and Backspace", () => {
		const { page, sent } = typing();
		page.observe(text(""), 1, "human");
		page.key("Backspace");
		expect(sent).toEqual([
			{ type: "input", payload: { kind: "key", phase: "down", key: "Backspace", code: "Backspace" } },
			{ type: "input", payload: { kind: "key", phase: "up", key: "Backspace", code: "Backspace" } },
		]);
	});

	it("starts again for another field of the same kind, such as a confirm-password field", () => {
		const { page, sent, typed } = typing();
		page.observe({ kind: "password", field: "t1:1:1" }, 1, "human");
		page.change("secret");
		// The page focuses the confirmation field: same kind, no value, another field.
		expect(page.observe({ kind: "password", field: "t1:1:2" }, 1, "human")).toBe("");
		sent.length = 0;
		page.change("secret");
		expect(typed()).toBe("secret");
	});

	it("starts again when another tab's field of the same kind has focus", () => {
		const { page } = typing();
		page.observe({ kind: "text", value: "one", field: "t1:1:1" }, 1, "human");
		page.change("one!");
		expect(page.observe({ kind: "text", value: "two", field: "t2:1:1" }, 1, "human")).toBe("two");
	});

	it("keeps the phone's text when the same field echoes its value", () => {
		const { page } = typing();
		page.observe({ kind: "text", value: "", field: "t1:1:1" }, 1, "human");
		page.change("ab");
		expect(page.observe({ kind: "text", value: "a", field: "t1:1:1" }, 1, "human")).toBeNull();
	});
});
