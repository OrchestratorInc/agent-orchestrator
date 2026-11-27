import { existsSync, watch, type FSWatcher } from "node:fs";
import { mkdir, readdir, readFile, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { ExpectedIpcError } from "./main-error-capture";
import {
	CUSTOM_THEME_MAX_BYTES,
	CUSTOM_THEMES_CHANGED_CHANNEL,
	CUSTOM_THEMES_LIST_CHANNEL,
	CUSTOM_THEMES_OPEN_FOLDER_CHANNEL,
	parseCustomTheme,
	type CustomThemeList,
} from "../shared/custom-themes";

const RELOAD_DEBOUNCE_MS = 100;

export function customThemesDir(env: Record<string, string | undefined> = process.env): string {
	return path.join(env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"), "themes");
}

/** Every `*.json` in `directory`, parsed; a missing folder is an empty list. */
export async function readCustomThemes(directory: string): Promise<CustomThemeList> {
	const list: CustomThemeList = { directory, themes: [], errors: [] };
	let files: string[];
	try {
		files = (await readdir(directory)).filter((file) => /\.json$/i.test(file)).sort();
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return list;
		throw error;
	}
	for (const file of files) {
		const target = path.join(directory, file);
		const text = await stat(target)
			.then((info) => (info.size > CUSTOM_THEME_MAX_BYTES ? undefined : readFile(target, "utf8")))
			.catch(() => undefined);
		if (text === undefined) {
			list.errors.push({ file, problem: { code: "read" } });
			continue;
		}
		const parsed = parseCustomTheme(file, text);
		if ("problem" in parsed) list.errors.push(parsed);
		// Sunset.json and sunset.json share an id on a case-sensitive disk.
		else if (list.themes.some((theme) => theme.id === parsed.id)) list.errors.push({ file, problem: { code: "duplicate" } });
		else list.themes.push(parsed);
	}
	return list;
}

export function registerCustomThemesIpc(
	ipcMain: Pick<Electron.IpcMain, "handle">,
	deps: {
		directory: string;
		broadcast: (channel: string, list: CustomThemeList) => void;
		openPath: (target: string) => Promise<string>;
	},
): void {
	let watcher: FSWatcher | undefined;
	let reload: NodeJS.Timeout | undefined;

	const watchDirectory = async () => {
		if (watcher) return;
		await mkdir(deps.directory, { recursive: true });
		watcher = watch(deps.directory, () => {
			clearTimeout(reload);
			reload = setTimeout(() => void reloadThemes(), RELOAD_DEBOUNCE_MS);
		});
		watcher.on("error", () => undefined);
	};

	const reloadThemes = async () => {
		// A watcher does not follow a folder that was deleted and made again.
		if (!existsSync(deps.directory)) {
			watcher?.close();
			watcher = undefined;
			await watchDirectory();
		}
		deps.broadcast(CUSTOM_THEMES_CHANGED_CHANNEL, await readCustomThemes(deps.directory));
	};

	ipcMain.handle(CUSTOM_THEMES_LIST_CHANNEL, async () => {
		await watchDirectory();
		return readCustomThemes(deps.directory);
	});
	ipcMain.handle(CUSTOM_THEMES_OPEN_FOLDER_CHANNEL, async () => {
		await watchDirectory();
		const error = await deps.openPath(deps.directory);
		if (error) throw new ExpectedIpcError(error);
	});
}
