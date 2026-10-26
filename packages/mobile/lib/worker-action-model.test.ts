import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
	ARCHIVE_SESSION_HINT,
	ARCHIVE_SESSION_CONFIRM,
	workerActionSymbol,
	workerActionSymbols,
	workerContextActions,
	type WorkerActionId,
	type WorkerActionState,
} from "./worker-action-model";

const ALL_ACTIONS: readonly WorkerActionId[] = ["open", "pin", "unpin", "rename", "resume", "restore", "openPr", "delete"];

const state = (over: Partial<WorkerActionState> = {}): WorkerActionState => ({
	pinned: false,
	terminated: false,
	stopped: false,
	hasPr: false,
	...over,
});

const ids = (over: Partial<WorkerActionState> = {}) => workerContextActions(state(over)).map((a) => a.id);

describe("workerContextActions", () => {
	it("offers the always-available actions on a plain running worker", () => {
		expect(ids()).toEqual(["open", "pin", "rename", "delete"]);
	});

	it("flips pin to unpin for a pinned worker", () => {
		expect(ids({ pinned: true })).toContain("unpin");
		expect(ids({ pinned: true })).not.toContain("pin");
	});

	it("offers the pull request only when there is one", () => {
		const action = workerContextActions(state({ hasPr: true })).find((item) => item.id === "openPr");
		expect(action).toEqual({ id: "openPr", title: "Review pull request" });
		expect(ids()).not.toContain("openPr");
	});

	// The chat screen already makes this choice: a terminated AO session is
	// restored, a merely stopped agent is resumed. Offering both would push that
	// distinction onto the user.
	it("offers resume for a stopped agent and restore for a terminated session", () => {
		expect(ids({ stopped: true })).toContain("resume");
		expect(ids({ stopped: true })).not.toContain("restore");
		expect(ids({ terminated: true })).toContain("restore");
		expect(ids({ terminated: true })).not.toContain("resume");
	});

	it("prefers restore when a session is both terminated and stopped", () => {
		expect(ids({ terminated: true, stopped: true })).toContain("restore");
		expect(ids({ terminated: true, stopped: true })).not.toContain("resume");
	});

	// Regression fence: there is no stop or pause endpoint. kill() is what Delete
	// calls, so a "stop" entry would be a second name for the same destructive
	// action.
	it("never offers an action without an endpoint behind it", () => {
		const everything = [
			...ids(),
			...ids({ pinned: true, hasPr: true, stopped: true }),
			...ids({ terminated: true, hasPr: true }),
		];
		expect(everything).not.toContain("stop");
		expect(everything).not.toContain("pause");
	});

	// Archive calls killSession, which deletes a clean worktree folder.
	it("keeps Archive last and marks it as the only destructive action", () => {
		for (const over of [{}, { pinned: true, hasPr: true }, { terminated: true }]) {
			const actions = workerContextActions(state(over));
			expect(actions.at(-1)).toEqual({ id: "delete", title: "Archive session", destructive: true });
			expect(actions.filter((a) => a.destructive)).toHaveLength(1);
		}
	});

	it("confirms Archive with desktop's wording", () => {
		expect(ARCHIVE_SESSION_CONFIRM).toEqual({
			title: "Are you sure you want to archive this session?",
			message: "You can always restore it from the Archive section later.",
			cancel: "No",
			confirm: "Confirm",
		});
		expect(ARCHIVE_SESSION_HINT).toMatch(/Deletes the worktree folder unless it has uncommitted changes/);
	});

	it("opens with Open, so the menu's first item is the row's own tap", () => {
		expect(ids({ terminated: true, hasPr: true, pinned: true })[0]).toBe("open");
	});
});

describe("workerActionSymbol", () => {
	it("gives every action a symbol on each platform", () => {
		for (const id of ALL_ACTIONS) {
			expect(workerActionSymbol(id).ios.length).toBeGreaterThan(0);
			expect(workerActionSymbol(id).android.length).toBeGreaterThan(0);
		}
	});

	it("distinguishes pin from unpin", () => {
		expect(workerActionSymbol("pin")).not.toEqual(workerActionSymbol("unpin"));
	});

	it("uses the archive box for the restorable session action", () => {
		expect(workerActionSymbol("delete")).toEqual({ ios: "archivebox", android: "archive" });
	});

	// Android's menu takes a bundled drawable, and a missing file is a
	// bundle-time failure rather than a blank icon, so every action's Material
	// Symbol needs its file, written from the font by the generator, and its
	// require in the map the menus draw from.
	it.each(ALL_ACTIONS)("bundles %s's Material Symbol as a drawable", (id) => {
		const name = workerActionSymbols[id].android;
		const path = fileURLToPath(new URL(`../assets/icons/${name}.xml`, import.meta.url));
		expect(existsSync(path)).toBe(true);
		expect(readFileSync(path, "utf8")).toContain('android:viewportWidth="960"');
		expect(readFileSync(fileURLToPath(new URL("./worker-action-drawables.ts", import.meta.url)), "utf8"))
			.toContain(`${name}: require("../assets/icons/${name}.xml")`);
	});
});
