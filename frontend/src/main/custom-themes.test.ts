import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { THEMES } from "../../../packages/shared/theme/palettes";
import { CUSTOM_THEME_MAX_BYTES, CUSTOM_THEMES_CHANGED_CHANNEL, CUSTOM_THEMES_LIST_CHANNEL, type CustomThemeList } from "../shared/custom-themes";
import { customThemesDir, readCustomThemes, registerCustomThemesIpc } from "./custom-themes";

const [, vercel] = THEMES;
const theme = (name: string) => JSON.stringify({ name, dark: vercel.dark, light: vercel.light });

let root: string;
let directory: string;

beforeEach(async () => {
	root = await mkdtemp(path.join(os.tmpdir(), "ao-themes-"));
	directory = path.join(root, "themes");
});

afterEach(async () => {
	await rm(root, { recursive: true, force: true });
});

describe("customThemesDir", () => {
	it("stays under the AO data dir", () => {
		expect(customThemesDir({ AO_DATA_DIR: " /tmp/lab " })).toBe(path.join("/tmp/lab", "themes"));
		expect(customThemesDir({})).toBe(path.join(os.homedir(), ".ao", "themes"));
	});
});

describe("readCustomThemes", () => {
	it("is empty when the folder does not exist", async () => {
		expect(await readCustomThemes(directory)).toEqual({ directory, themes: [], errors: [] });
	});

	it("lists valid themes and reports each file it could not load", async () => {
		await mkdir(path.join(directory, "folder.json"), { recursive: true });
		await writeFile(path.join(directory, "b.json"), theme("B"));
		await writeFile(path.join(directory, "a.json"), theme("A"));
		await writeFile(path.join(directory, "broken.json"), "{");
		await writeFile(path.join(directory, "huge.json"), `${theme("Huge")}${" ".repeat(CUSTOM_THEME_MAX_BYTES)}`);
		await writeFile(path.join(directory, "notes.txt"), "not a theme");

		const list = await readCustomThemes(directory);

		expect(list.themes.map(({ id, label }) => [id, label])).toEqual([["custom:a", "A"], ["custom:b", "B"]]);
		expect(list.errors).toEqual([
			{ file: "broken.json", problem: { code: "json" } },
			{ file: "folder.json", problem: { code: "read" } },
			{ file: "huge.json", problem: { code: "read" } },
		]);
	});
});

describe("registerCustomThemesIpc", () => {
	function register() {
		const handlers = new Map<string, () => Promise<unknown>>();
		const broadcast = vi.fn<(channel: string, list: CustomThemeList) => void>();
		registerCustomThemesIpc({ handle: (channel, handler) => void handlers.set(channel, handler as () => Promise<unknown>) }, {
			directory,
			broadcast,
			openPath: async () => "",
		});
		return { list: handlers.get(CUSTOM_THEMES_LIST_CHANNEL)!, broadcast };
	}

	// macOS drops events from the first moments of a watch, so write until one lands.
	async function startWatching(list: () => Promise<unknown>, broadcast: ReturnType<typeof vi.fn>) {
		const first = await list();
		await vi.waitFor(async () => {
			await writeFile(path.join(directory, "probe.json"), theme("Probe"));
			expect(broadcast).toHaveBeenCalled();
		}, { timeout: 10_000, interval: 200 });
		await rm(path.join(directory, "probe.json"));
		await vi.waitFor(() => expect(broadcast).toHaveBeenLastCalledWith(CUSTOM_THEMES_CHANGED_CHANNEL, { directory, themes: [], errors: [] }), { timeout: 10_000 });
		return first;
	}

	it("creates the folder, then sends the new list when a theme file is written or removed", async () => {
		const { list, broadcast } = register();
		expect(await startWatching(list, broadcast)).toEqual({ directory, themes: [], errors: [] });

		await writeFile(path.join(directory, "sunset.json"), theme("Sunset"));
		await vi.waitFor(() => expect(broadcast).toHaveBeenLastCalledWith(
			CUSTOM_THEMES_CHANGED_CHANNEL,
			expect.objectContaining({ themes: [expect.objectContaining({ id: "custom:sunset", label: "Sunset" })] }),
		), { timeout: 10_000 });

		await rm(path.join(directory, "sunset.json"));
		await vi.waitFor(() => expect(broadcast).toHaveBeenLastCalledWith(CUSTOM_THEMES_CHANGED_CHANNEL, { directory, themes: [], errors: [] }), { timeout: 10_000 });
	}, 30_000);

	it("keeps watching after the folder is deleted and made again", async () => {
		const { list, broadcast } = register();
		await startWatching(list, broadcast);
		broadcast.mockClear();
		await rm(directory, { recursive: true });
		await vi.waitFor(() => expect(broadcast).toHaveBeenCalled(), { timeout: 10_000 });

		await vi.waitFor(async () => {
			await writeFile(path.join(directory, "again.json"), theme("Again"));
			expect(broadcast).toHaveBeenLastCalledWith(
				CUSTOM_THEMES_CHANGED_CHANNEL,
				expect.objectContaining({ themes: [expect.objectContaining({ id: "custom:again" })] }),
			);
		}, { timeout: 10_000, interval: 200 });
	}, 30_000);
});
