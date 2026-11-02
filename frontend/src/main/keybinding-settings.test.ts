import { describe, expect, it } from "vitest";
import { coerceKeybindingOverrides } from "./keybinding-settings";

describe("coerceKeybindingOverrides", () => {
	it("keeps valid application bindings and ignores unknown commands", () => {
		expect(
			coerceKeybindingOverrides({
				"toggle-terminal": [{ key: "j", ctrl: true }],
				"unknown-command": [{ key: "q", ctrl: true }],
			}),
		).toEqual({
			"toggle-terminal": [
				{ key: "j", ctrl: true, meta: false, shift: false, alt: false },
			],
		});
	});

	it("falls back to defaults for corrupt non-empty arrays but preserves an intentional unassignment", () => {
		expect(
			coerceKeybindingOverrides({
				"open-settings": [{ key: "s" }],
				"command-palette": [],
			}),
		).toEqual({
			"command-palette": [],
		});
	});

	it("rejects standalone function keys and terminal-critical chords", () => {
		expect(
			coerceKeybindingOverrides({
				"toggle-terminal": [{ key: "F6" }],
				"open-settings": [{ key: "c", ctrl: true }],
			}),
		).toEqual({});
	});

	it("does not accept overrides for the fixed indexed project shortcut", () => {
		expect(
			coerceKeybindingOverrides({
				"jump-to-session": [{ key: "p", ctrl: true }],
			}),
		).toEqual({});
	});

	it("applies platform-aware reserved shortcut validation", () => {
		expect(coerceKeybindingOverrides({ "toggle-terminal": [{ key: "q", meta: true }] }, true)).toEqual({});
		expect(coerceKeybindingOverrides({ "toggle-terminal": [{ key: "q", meta: true }] }, false)).toEqual({
			"toggle-terminal": [{ key: "q", ctrl: false, meta: true, shift: false, alt: false }],
		});
	});
});
