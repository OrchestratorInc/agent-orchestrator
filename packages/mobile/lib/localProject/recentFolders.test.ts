import { beforeEach, describe, expect, it, vi } from "vitest";
import { loadProjectPlaces, parentFolder, rememberProjectPlace, resetProjectPlacesCache, withRecentPlace } from "./recentFolders";

const storage = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: async (key: string) => storage.get(key) ?? null,
		setItem: async (key: string, value: string) => { storage.set(key, value); },
	},
}));

beforeEach(() => {
	storage.clear();
	resetProjectPlacesCache();
});

describe("recent project places", () => {
	it("keeps the latest first, once each, up to the limit", () => {
		expect(withRecentPlace(["/a", "/b", "/c", "/d"], "/c")).toEqual(["/c", "/a", "/b", "/d"]);
		expect(withRecentPlace(["/a", "/b", "/c", "/d"], "/e")).toEqual(["/e", "/a", "/b", "/c"]);
	});

	it("finds the folder a path sits in", () => {
		expect(parentFolder("/Users/me/app/")).toBe("/Users/me");
		expect(parentFolder("/app")).toBe("/");
		expect(parentFolder("C:\\code\\app")).toBe("C:\\code");
		expect(parentFolder("C:\\app")).toBe("C:\\");
	});

	it("remembers per machine and survives a fresh read", async () => {
		await rememberProjectPlace("mac", { folder: "/Users/me/code/app" });
		await rememberProjectPlace("mac", { destination: "~/src" });
		await rememberProjectPlace("studio", { folder: "/srv/x" });
		resetProjectPlacesCache();
		expect(await loadProjectPlaces("mac")).toEqual({ recent: ["/Users/me/code"], destination: "~/src" });
		expect(await loadProjectPlaces("studio")).toEqual({ recent: ["/srv"] });
		expect(await loadProjectPlaces("other")).toEqual({ recent: [] });
	});

	it("ignores a stored value it can't read", async () => {
		storage.set("ao.projectPlaces.mac", "{nope");
		expect(await loadProjectPlaces("mac")).toEqual({ recent: [] });
	});
});
