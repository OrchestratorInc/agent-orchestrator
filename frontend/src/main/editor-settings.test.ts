// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
	DEFAULT_EDITOR_ID,
	EDITOR_SETTINGS_FILE_NAME,
	readEditorSettings,
	writeEditorPreference,
	writeNeovimTerminalPreference,
} from "./editor-settings";

describe("editor settings", () => {
	let stateDir: string;

	beforeEach(async () => {
		stateDir = await mkdtemp(path.join(os.tmpdir(), "ao-editor-settings-"));
	});

	afterEach(async () => {
		await rm(stateDir, { recursive: true, force: true });
	});

	it("defaults to Cursor when the preference is absent or invalid", async () => {
		expect(await readEditorSettings(stateDir)).toEqual({ preferredEditorId: DEFAULT_EDITOR_ID, neovimTerminal: "terminal" });
		await writeFile(path.join(stateDir, EDITOR_SETTINGS_FILE_NAME), `{"preferredEditorId":"unknown"}`);
		expect(await readEditorSettings(stateDir)).toEqual({ preferredEditorId: "cursor", neovimTerminal: "terminal" });
	});

	it("atomically persists a supported preferred editor under AO state", async () => {
		await writeEditorPreference(stateDir, "vscode");
		expect(await readEditorSettings(stateDir)).toEqual({ preferredEditorId: "vscode", neovimTerminal: "terminal" });
		const raw = await readFile(path.join(stateDir, EDITOR_SETTINGS_FILE_NAME), "utf8");
		expect(JSON.parse(raw)).toEqual({ preferredEditorId: "vscode", neovimTerminal: "terminal" });
	});
	it("preserves both preferences across concurrent writes and rereads", async () => {
		await Promise.all([writeNeovimTerminalPreference(stateDir, "iterm2"), writeEditorPreference(stateDir, "neovim")]);
		expect(await readEditorSettings(stateDir)).toEqual({ preferredEditorId: "neovim", neovimTerminal: "iterm2" });
		await writeEditorPreference(stateDir, "vscode");
		expect(await readEditorSettings(stateDir)).toEqual({ preferredEditorId: "vscode", neovimTerminal: "iterm2" });
	});

	it("migrates older settings and rejects unsupported terminal IDs", async () => {
		await writeFile(path.join(stateDir, EDITOR_SETTINGS_FILE_NAME), JSON.stringify({ preferredEditorId: "neovim", neovimTerminal: "command; bad" }));
		expect(await readEditorSettings(stateDir)).toEqual({ preferredEditorId: "neovim", neovimTerminal: "terminal" });
	});
});
