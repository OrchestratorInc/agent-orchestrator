import { describe, expect, it } from "vitest";
import {
	forcesTerminalSelection,
	isForwardableMouseReport,
	isMouseHoverReport,
	parseOsc52Write,
	shouldForceTerminalSelection,
} from "./terminal-mouse";

const press = (init: Partial<Pick<MouseEvent, "altKey" | "shiftKey" | "button">> = {}) => ({
	altKey: false,
	shiftKey: false,
	button: 0,
	...init,
});

describe("forcesTerminalSelection", () => {
	it("uses Option on macOS and Shift elsewhere", () => {
		expect(forcesTerminalSelection(press({ altKey: true }), true)).toBe(true);
		expect(forcesTerminalSelection(press({ shiftKey: true }), true)).toBe(false);
		expect(forcesTerminalSelection(press({ shiftKey: true }), false)).toBe(true);
		expect(forcesTerminalSelection(press({ altKey: true }), false)).toBe(false);
	});
});

describe("shouldForceTerminalSelection", () => {
	const app = { mode: "app" as const, mac: true, overLink: false };

	it("reports a plain primary press in app mode", () => {
		expect(shouldForceTerminalSelection(press(), app)).toBe(false);
	});

	it("keeps the modifier, non-primary buttons and link presses local", () => {
		expect(shouldForceTerminalSelection(press({ altKey: true }), app)).toBe(true);
		expect(shouldForceTerminalSelection(press({ button: 1 }), app)).toBe(true);
		expect(shouldForceTerminalSelection(press({ button: 2 }), app)).toBe(true);
		expect(shouldForceTerminalSelection(press(), { ...app, overLink: true })).toBe(true);
	});

	it("always selects in select mode", () => {
		expect(shouldForceTerminalSelection(press(), { ...app, mode: "select" })).toBe(true);
	});
});

describe("isForwardableMouseReport", () => {
	it("accepts single SGR press, release and motion reports", () => {
		expect(isForwardableMouseReport("\x1b[<0;1;1M")).toBe(true);
		expect(isForwardableMouseReport("\x1b[<0;120;40m")).toBe(true);
		expect(isForwardableMouseReport("\x1b[<32;3;4M")).toBe(true);
		expect(isForwardableMouseReport("\x1b[<35;3;4M")).toBe(true);
		expect(isForwardableMouseReport("\x1b[<4;2;2M")).toBe(true);
	});

	it("rejects wheel reports, which AO synthesizes itself", () => {
		expect(isForwardableMouseReport("\x1b[<64;1;1M")).toBe(false);
		expect(isForwardableMouseReport("\x1b[<65;1;1M")).toBe(false);
		expect(isForwardableMouseReport("\x1b[M`!!")).toBe(false);
	});

	it("accepts 7-bit X10 reports and drops ones the text path cannot carry", () => {
		expect(isForwardableMouseReport("\x1b[M !!")).toBe(true);
		expect(isForwardableMouseReport("\x1b[M#!!")).toBe(true);
		expect(isForwardableMouseReport("\x1b[M \x80!")).toBe(false);
	});

	it("rejects anything that is not exactly one report", () => {
		for (const data of ["", "a", "\x1b[<0;1;1", "\x1b[<0;1;1Mx", "\x1b[<0;1;1M\x1b[<0;1;1m", "\x1b[A", "\x1b[7;21R", "\x1b[M!!"]) {
			expect(isForwardableMouseReport(data), JSON.stringify(data)).toBe(false);
		}
	});
});

describe("isMouseHoverReport", () => {
	it("recognizes motion with no button held, with or without modifiers", () => {
		expect(isMouseHoverReport("\x1b[<35;5;5M")).toBe(true);
		expect(isMouseHoverReport("\x1b[<39;5;5M")).toBe(true);
		expect(isMouseHoverReport("\x1b[MC!!")).toBe(true);
	});

	it("does not treat presses, releases or drags as hover", () => {
		for (const data of ["\x1b[<0;5;5M", "\x1b[<0;5;5m", "\x1b[<32;5;5M", "\x1b[<34;5;5M", "\x1b[M !!", "\x1b[M@!!", "x"]) {
			expect(isMouseHoverReport(data), JSON.stringify(data)).toBe(false);
		}
	});
});

describe("parseOsc52Write", () => {
	const encode = (text: string) => btoa(String.fromCharCode(...new TextEncoder().encode(text)));

	it("decodes UTF-8 clipboard writes for any selection target", () => {
		expect(parseOsc52Write(`c;${encode("hello")}`)).toBe("hello");
		expect(parseOsc52Write(`;${encode("tmux default")}`)).toBe("tmux default");
		expect(parseOsc52Write(`pc;${encode("héllo ✓ 你好")}`)).toBe("héllo ✓ 你好");
	});

	it("never treats a query, a clear or malformed data as a write", () => {
		expect(parseOsc52Write("c;?")).toBeNull();
		expect(parseOsc52Write("c;")).toBeNull();
		expect(parseOsc52Write("c")).toBeNull();
		expect(parseOsc52Write("x;aGVsbG8=")).toBeNull();
		expect(parseOsc52Write("c;not base64!")).toBeNull();
		expect(parseOsc52Write(`c;${btoa("\xff\xfe")}`)).toBeNull();
	});
});
