import path from "node:path";
import { describe, expect, it } from "vitest";
import { crashDumpsDir, formatAppMetrics } from "./process-diagnostics";

describe("crashDumpsDir", () => {
	it("keeps crash dumps inside the pinned Electron profile", () => {
		expect(crashDumpsDir("/tmp/ao-electron-profile")).toBe(path.join("/tmp/ao-electron-profile", "Crashpad"));
	});
});

describe("formatAppMetrics", () => {
	it("lists processes largest first and marks the shell renderer", () => {
		const line = formatAppMetrics(
			[
				{ pid: 10, type: "Browser", memory: { workingSetSize: 200 * 1024, peakWorkingSetSize: 250 * 1024 } },
				{ pid: 11, type: "Tab", memory: { workingSetSize: 2_400 * 1024, peakWorkingSetSize: 2_500 * 1024 } },
				{ pid: 12, type: "Utility", serviceName: "network.mojom.NetworkService", memory: { workingSetSize: 40 * 1024, peakWorkingSetSize: 41 * 1024 } },
			],
			11,
		);
		expect(line).toBe(
			"AO: process metrics total_ws=2640MB | Tab:11(shell) ws=2400MB peak=2500MB | Browser:10(Browser) ws=200MB peak=250MB | Utility:12(network.mojom.NetworkService) ws=40MB peak=41MB",
		);
	});

	it("includes private bytes where the platform reports them", () => {
		const line = formatAppMetrics([
			{ pid: 7, type: "GPU", memory: { workingSetSize: 1024, peakWorkingSetSize: 2048, privateBytes: 3072 } },
		]);
		expect(line).toBe("AO: process metrics total_ws=1MB | GPU:7(GPU) ws=1MB peak=2MB private=3MB");
	});
});
