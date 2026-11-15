import { expect, test, vi } from "vitest";
import { readLastRunCue, rememberLastRunCue } from "./last-run-cue";

test("last-run preference survives module reload and is isolated by project", async () => {
	rememberLastRunCue("history-a", "cue-a");
	rememberLastRunCue("history-b", "cue-b");
	vi.resetModules();
	const reloaded = await import("./last-run-cue");
	expect(reloaded.readLastRunCue("history-a")).toBe("cue-a");
	expect(reloaded.readLastRunCue("history-b")).toBe("cue-b");
	expect(reloaded.readLastRunCue("history-unset")).toBeNull();
});

test("unavailable storage does not fail dispatch and remembers in memory", () => {
	const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("unavailable"); });
	const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("unavailable"); });
	try {
		expect(() => rememberLastRunCue("history-unavailable", "cue-memory")).not.toThrow();
		expect(readLastRunCue("history-unavailable")).toBe("cue-memory");
	} finally {
		get.mockRestore();
		set.mockRestore();
	}
});
