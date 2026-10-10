import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const bridge = readFileSync(fileURLToPath(new URL("../../modules/ao-watch/ios/AOWatchModule.swift", import.meta.url)), "utf8");

// ponytail: source guard because Mobile CI cannot compile the UIKit/WatchConnectivity bridge; replace with a native test once CI builds iOS.
describe("AOWatch native bridge readiness", () => {
	it("leaves JS-listener readiness to the WatchManager handshake, not the WCSession lifecycle", () => {
		const writes = bridge.split("\n").filter((line) => /\bready\s*=[^=]/.test(line) && !/\bvar ready\b/.test(line));
		expect(writes.length).toBeGreaterThan(0);
		for (const line of writes) expect(line).toMatch(/AsyncFunction\("setReady"\)|OnDestroy/);
	});
	it("re-activates after a paired-Watch switch without needing an AppState change", () => {
		expect(bridge).toMatch(/func sessionDidBecomeInactive\(_ session: WCSession\) \{\}/);
		expect(bridge).toMatch(/func sessionDidDeactivate\(_ session: WCSession\) \{ session\.activate\(\) \}/);
		expect(bridge).toMatch(/UIApplication\.shared\.applicationState == \.active/);
	});
});
