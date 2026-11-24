import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { isEditorId, isNeovimTerminalId, type EditorId, type NeovimTerminalId } from "../shared/editor-handoff";

export const EDITOR_SETTINGS_FILE_NAME = "editor-settings.json";
export const DEFAULT_EDITOR_ID: EditorId = "cursor";

export type EditorSettings = {
	preferredEditorId: EditorId;
	neovimTerminal: NeovimTerminalId;
};

let settingsOperationQueue: Promise<void> = Promise.resolve();

export function coerceEditorSettings(raw: unknown): EditorSettings {
	if (!raw || typeof raw !== "object") return { preferredEditorId: DEFAULT_EDITOR_ID, neovimTerminal: "terminal" };
	const { preferredEditorId, neovimTerminal } = raw as Record<string, unknown>;
	return {
		preferredEditorId: isEditorId(preferredEditorId) ? preferredEditorId : DEFAULT_EDITOR_ID,
		neovimTerminal: isNeovimTerminalId(neovimTerminal) ? neovimTerminal : "terminal",
	};
}

async function readUnlocked(stateDir: string): Promise<EditorSettings> {
	try {
		const raw = await readFile(path.join(stateDir, EDITOR_SETTINGS_FILE_NAME), "utf8");
		return coerceEditorSettings(JSON.parse(raw));
	} catch {
		return { preferredEditorId: DEFAULT_EDITOR_ID, neovimTerminal: "terminal" };
	}
}

async function writeUnlocked(stateDir: string, update: Partial<EditorSettings>): Promise<EditorSettings> {
	const next = coerceEditorSettings({ ...await readUnlocked(stateDir), ...update });
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, EDITOR_SETTINGS_FILE_NAME);
	const temporary = path.join(stateDir, `.editor-settings-${process.pid}-${Date.now()}.json`);
	await writeFile(temporary, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
	await rename(temporary, file);
	return next;
}

function runSettingsOperation<T>(operation: () => Promise<T>): Promise<T> {
	const queued = settingsOperationQueue.then(operation, operation);
	settingsOperationQueue = queued.then(
		() => undefined,
		() => undefined,
	);
	return queued;
}

export function readEditorSettings(stateDir: string): Promise<EditorSettings> {
	return readUnlocked(stateDir);
}

export function writeEditorPreference(stateDir: string, preferredEditorId: EditorId): Promise<EditorSettings> {
	return runSettingsOperation(() => writeUnlocked(stateDir, { preferredEditorId }));
}

export function writeNeovimTerminalPreference(stateDir: string, neovimTerminal: NeovimTerminalId): Promise<EditorSettings> {
	return runSettingsOperation(() => writeUnlocked(stateDir, { neovimTerminal }));
}
