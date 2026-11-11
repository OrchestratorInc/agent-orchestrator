import { execFileSync } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { markDownloaded, quarantineValue } from "./quarantine";

describe("quarantineValue", () => {
	it("reads as a browser download: flags, hex seconds, the app", () => {
		expect(quarantineValue("Orchestrator", Date.UTC(2026, 10, 3) )).toBe(`0083;${Math.floor(Date.UTC(2026, 10, 3) / 1000).toString(16)};Orchestrator;`);
		expect(quarantineValue("A;B", 0)).toBe("0083;0;AB;");
	});
});

describe("markDownloaded", () => {
	it("sets com.apple.quarantine with xattr, without a shell, on macOS only", async () => {
		const run = vi.fn(async () => undefined);
		await markDownloaded("/Users/me/Downloads/it's $(id).txt", "Orchestrator", { platform: "darwin", run, now: 16_000 });
		expect(run).toHaveBeenCalledExactlyOnceWith("/usr/bin/xattr", ["-w", "com.apple.quarantine", "0083;10;Orchestrator;", "/Users/me/Downloads/it's $(id).txt"]);
		run.mockClear();
		await markDownloaded("/home/me/a.txt", "Orchestrator", { platform: "linux", run });
		await markDownloaded("C:\\\\a.txt", "Orchestrator", { platform: "win32", run });
		expect(run).not.toHaveBeenCalled();
	});

	it.skipIf(process.platform !== "darwin")("leaves the file quarantined as macOS reads it", async () => {
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-quarantine-"));
		try {
			const file = path.join(dir, "it's a $(id) file.txt");
			await writeFile(file, "x");
			await markDownloaded(file, "Orchestrator");
			expect(execFileSync("/usr/bin/xattr", ["-p", "com.apple.quarantine", file], { encoding: "utf8" }).trim()).toMatch(/^0083;[0-9a-f]+;Orchestrator;$/);
		} finally {
			await rm(dir, { recursive: true, force: true });
		}
	});
});
