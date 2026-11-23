import { describe, expect, it } from "vitest";
import { commandSupported, toViewState, type BrowserCapabilities, type BrowserCommand } from "./browserTransport";

const page = {
	url: "https://example.com/",
	title: "Example",
	loading: false,
	canGoBack: false,
	canGoForward: false,
	activeTabId: "t1",
	tabs: [],
	width: 1280,
	height: 800,
};

const none: BrowserCapabilities = { text: false, keys: false, viewport: false, quality: false, control: false, dialogs: false };
const all: BrowserCapabilities = { text: true, keys: true, viewport: true, quality: true, control: true, dialogs: true };

describe("transport contract", () => {
	it("defaults control to none and focusedEditable to null for older desktops", () => {
		expect(toViewState(page)).toEqual({ ...page, control: "none", focusedEditable: null });
	});

	it("keeps control and focusedEditable when the host reports them", () => {
		const state = toViewState({ ...page, control: "human", focusedEditable: { kind: "password" } });
		expect(state.control).toBe("human");
		expect(state.focusedEditable).toEqual({ kind: "password" });
	});

	it("rejects an unknown control value", () => {
		expect(toViewState({ ...page, control: "robot" as never }).control).toBe("none");
	});

	const gated: Array<[keyof BrowserCapabilities, BrowserCommand]> = [
		["text", { type: "input", payload: { kind: "text", value: "a" } }],
		["keys", { type: "input", payload: { kind: "key", phase: "down", key: "Enter" } }],
		["viewport", { type: "viewport", payload: null }],
		["quality", { type: "quality", payload: { quality: 50, maxFps: 15 } }],
		["control", { type: "control", payload: { action: "take" } }],
		["dialogs", { type: "dialog", payload: { accept: true } }],
	];

	it.each(gated)("treats a %s command as unsupported when the capability is false", (capability, command) => {
		expect(commandSupported(none, command)).toBe(false);
		expect(commandSupported({ ...none, [capability]: true }, command)).toBe(true);
	});

	it.each<BrowserCommand>([
		{ type: "input", payload: { kind: "pointer", phase: "down", x: 0.5, y: 0.5, button: "left" } },
		{ type: "input", payload: { kind: "wheel", deltaX: 0, deltaY: 10 } },
		{ type: "navigate", payload: { action: "back" } },
		{ type: "tab", payload: { action: "new" } },
	])("always supports the baseline command %j", (command) => {
		expect(commandSupported(none, command)).toBe(true);
		expect(commandSupported(all, command)).toBe(true);
	});
});
