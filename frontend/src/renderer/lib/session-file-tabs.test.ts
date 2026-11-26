import { describe, expect, it } from "vitest";
import { EMPTY_SESSION_FILE_TABS, activateSessionFile, closeAllSessionFiles, closeSessionFile, openSessionFile } from "./session-file-tabs";

describe("session file tabs", () => {
	it("opens each path once and activates it", () => {
		const opened = openSessionFile({ openPaths: [], activePath: null }, "src/App.tsx");
		expect(openSessionFile(opened, "src/App.tsx")).toEqual({
			openPaths: ["src/App.tsx"],
			activePath: "src/App.tsx",
		});
	});

	it("returns to the nearest file and then the agent surface when closing", () => {
		expect(closeSessionFile({ openPaths: ["a.ts", "b.ts"], activePath: "a.ts" }, "a.ts")).toEqual({
			openPaths: ["b.ts"],
			activePath: "b.ts",
		});
		expect(closeSessionFile({ openPaths: ["a.ts"], activePath: "a.ts" }, "a.ts")).toEqual({
			openPaths: [],
			activePath: null,
		});
	});

	it("keeps files open while the agent surface is active and closes all explicitly", () => {
		expect(activateSessionFile({ openPaths: ["a.ts"], activePath: "a.ts" }, null)).toEqual({
			openPaths: ["a.ts"],
			activePath: null,
		});
		expect(closeAllSessionFiles()).toEqual({ openPaths: [], activePath: null });
	});
});


it("keeps the workspace and different revisions of one path in separate tabs", async () => {
	const { sessionFileTabKey } = await import("./session-file-tabs");
	const path = "src/App.tsx";
	const source = { kind: "snapshot" as const, view: { branch: "feature", expectedTipSha: "a".repeat(40) }, label: "feature" };
	const snapshot = sessionFileTabKey(path, source);
	const newer = sessionFileTabKey(path, { ...source, view: { ...source.view, expectedTipSha: "b".repeat(40) } });
	expect(sessionFileTabKey(path)).toBe(path);
	expect(snapshot).not.toBe(path);
	expect(newer).not.toBe(snapshot);
	let tabs = openSessionFile(EMPTY_SESSION_FILE_TABS, path);
	tabs = openSessionFile(tabs, snapshot);
	tabs = openSessionFile(tabs, newer);
	expect(tabs.openPaths).toEqual([path, snapshot, newer]);
	expect(closeSessionFile(tabs, newer).activePath).toBe(snapshot);
});
