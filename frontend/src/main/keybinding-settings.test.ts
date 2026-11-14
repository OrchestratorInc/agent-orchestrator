import { describe, expect, it } from "vitest";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { coerceKeybindingOverrides, readKeybindingOverrides, writeKeybindingOverrides } from "./keybinding-settings";

describe("coerceKeybindingOverrides", () => {
	it("loads saved selection bindings, unassignment and reset after restarting", async () => {
		const parent = path.join(os.homedir(), ".ao", "dev");
		await mkdir(parent, { recursive: true });
		const stateDir = await mkdtemp(path.join(parent, "keybinding-settings-"));
		try {
			for (const overrides of [{ "add-selection-to-chat": [{ key: "j", ctrl: true, meta: false, shift: true, alt: true }] }, { "add-selection-to-chat": [] }, {}]) {
				await writeKeybindingOverrides(stateDir, overrides);
				expect(await readKeybindingOverrides(stateDir)).toEqual(overrides);
			}
		} finally {
			if (path.dirname(stateDir) === parent) await rm(stateDir, { recursive: true, force: true });
		}
	});
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
