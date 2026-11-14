import { describe, expect, it, vi } from "vitest";
import { APP_PRIVACY_SCRIPT, appBundlePath, parseAppPrivacy, readAppPrivacy, requestAppPrivacy } from "./app-privacy";

describe("readAppPrivacy", () => {
	it("asks a fresh osascript for both grants", async () => {
		const run = vi.fn().mockResolvedValue({ stdout: '{"accessibility":true,"screenRecording":false}\n' });
		await expect(readAppPrivacy(run)).resolves.toEqual({ accessibility: true, screenRecording: false });
		expect(run).toHaveBeenCalledWith("/usr/bin/osascript", ["-l", "JavaScript", "-e", APP_PRIVACY_SCRIPT]);
	});

	it("reads nothing when osascript fails or answers something else", async () => {
		await expect(readAppPrivacy(vi.fn().mockRejectedValue(new Error("exit 1")))).resolves.toBeUndefined();
		expect(parseAppPrivacy("execution error")).toBeUndefined();
		expect(parseAppPrivacy("null")).toBeUndefined();
		expect(parseAppPrivacy('{"accessibility":1,"screenRecording":true}')).toBeUndefined();
	});
});

describe("requestAppPrivacy", () => {
	it("prompts only for what is missing", async () => {
		const prompts = { accessibility: vi.fn(), screenRecording: vi.fn().mockResolvedValue(undefined) };
		await requestAppPrivacy({ accessibility: true, screenRecording: false }, prompts);
		expect(prompts.accessibility).not.toHaveBeenCalled();
		expect(prompts.screenRecording).toHaveBeenCalledTimes(1);

		await requestAppPrivacy({ accessibility: false, screenRecording: true }, prompts);
		expect(prompts.accessibility).toHaveBeenCalledTimes(1);
		expect(prompts.screenRecording).toHaveBeenCalledTimes(1);
	});
});

describe("appBundlePath", () => {
	it("finds the .app an executable runs from", () => {
		expect(appBundlePath("/Applications/Agent Orchestrator.app/Contents/MacOS/agent-orchestrator")).toBe("/Applications/Agent Orchestrator.app");
		expect(appBundlePath("/repo/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron")).toBe("/repo/node_modules/electron/dist/Electron.app");
		expect(appBundlePath("/usr/bin/node")).toBeUndefined();
	});
});
