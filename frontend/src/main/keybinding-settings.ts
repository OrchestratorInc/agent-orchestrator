import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import {
	APP_SHORTCUTS,
	shortcutBindingValidationError,
	type AppShortcutId,
	type KeybindingOverrides,
	type ShortcutBinding,
} from "../shared/shortcuts";

export const KEYBINDING_SETTINGS_FILE_NAME = "keybindings.json";

const customizableIds = new Set<AppShortcutId>(
	APP_SHORTCUTS.filter((shortcut) => shortcut.customizable !== false).map((shortcut) => shortcut.id),
);

let settingsOperationQueue: Promise<void> = Promise.resolve();

function coerceBinding(raw: unknown, isMac: boolean): ShortcutBinding | null {
	if (!raw || typeof raw !== "object") return null;
	const value = raw as Record<string, unknown>;
	if (typeof value.key !== "string" || value.key.length === 0 || value.key.length > 32) return null;
	const code = typeof value.code === "string" && value.code.length > 0 && value.code.length <= 32 ? value.code : undefined;
	const candidate: ShortcutBinding = {
		key: value.key,
		...(code ? { code } : {}),
		ctrl: value.ctrl === true,
		meta: value.meta === true,
		shift: value.shift === true,
		alt: value.alt === true,
	};
	if (shortcutBindingValidationError(candidate, isMac)) return null;
	return candidate;
}

export function coerceKeybindingOverrides(raw: unknown, isMac = process.platform === "darwin"): KeybindingOverrides {
	if (!raw || typeof raw !== "object") return {};
	const source = raw as Record<string, unknown>;
	const overrides: KeybindingOverrides = {};
	for (const id of customizableIds) {
		const rawBindings = source[id];
		if (!Array.isArray(rawBindings)) continue;
		const bindings = rawBindings
			.slice(0, 2)
			.map((binding) => coerceBinding(binding, isMac))
			.filter((candidate): candidate is ShortcutBinding => candidate !== null);
		if (rawBindings.length > 0 && bindings.length === 0) continue;
		overrides[id] = bindings;
	}
	return overrides;
}

async function readUnlocked(stateDir: string): Promise<KeybindingOverrides> {
	try {
		const raw = await readFile(path.join(stateDir, KEYBINDING_SETTINGS_FILE_NAME), "utf8");
		return coerceKeybindingOverrides(JSON.parse(raw));
	} catch {
		return {};
	}
}

async function writeUnlocked(stateDir: string, overrides: KeybindingOverrides): Promise<KeybindingOverrides> {
	const next = coerceKeybindingOverrides(overrides);
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, KEYBINDING_SETTINGS_FILE_NAME);
	const tmp = path.join(stateDir, `.keybindings-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
	await rename(tmp, file);
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

export async function readKeybindingOverrides(stateDir: string): Promise<KeybindingOverrides> {
	return readUnlocked(stateDir);
}

export async function writeKeybindingOverrides(
	stateDir: string,
	overrides: KeybindingOverrides,
): Promise<KeybindingOverrides> {
	return runSettingsOperation(() => writeUnlocked(stateDir, overrides));
}
