import { mkdirSync, mkdtempSync, rmSync, symlinkSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { expect, it } from "vitest";
import { isolatedLiveGridRoot } from "./live-grid-lab";

it("allows a dedicated lab root and rejects production and development state", () => {
	const home = path.join(os.tmpdir(), "ao-grid-fixture-home");
	const root = path.join(home, ".ao");
	expect(isolatedLiveGridRoot(path.join(root, "live-grid-lab"), home)).toBe(path.join(root, "live-grid-lab"));
	for (const directory of [root, ...["data", "electron", "br", "dev"].map((name) => path.join(root, name))]) {
		expect(() => isolatedLiveGridRoot(directory, home)).toThrow("own state directory");
	}
});

it("rejects aliases and state subdirectories that escape through symlinks", () => {
	const home = mkdtempSync(path.join(os.tmpdir(), "ao-grid-lab-guard-"));
	try {
		const production = path.join(home, ".ao");
		const lab = path.join(production, "live-grid-lab");
		mkdirSync(lab, { recursive: true });
		mkdirSync(path.join(production, "data"));
		const alias = path.join(home, "alias");
		symlinkSync(production, alias, process.platform === "win32" ? "junction" : "dir");
		expect(() => isolatedLiveGridRoot(alias, home)).toThrow("own state directory");
		symlinkSync(
			path.join(production, "data"),
			path.join(lab, "data"),
			process.platform === "win32" ? "junction" : "dir",
		);
		expect(() => isolatedLiveGridRoot(lab, home)).toThrow("inside its isolated directory");
	} finally {
		rmSync(home, { recursive: true, force: true });
	}
});

it.each(["remotes.json", "cloud-auth.bin", "app-state.json", "daemon.log", "update-settings.json"])("rejects a test profile credential/state alias: %s", (name) => {
	const home = mkdtempSync(path.join(os.tmpdir(), "ao-grid-profile-guard-"));
	try {
		const root = path.join(home, "test-profile");
		mkdirSync(root);
		symlinkSync(path.join(home, ".ao", name), path.join(root, name), "file");
		expect(() => isolatedLiveGridRoot(root, home)).toThrow("inside its isolated directory");
	} finally { rmSync(home, { recursive: true, force: true }); }
});
