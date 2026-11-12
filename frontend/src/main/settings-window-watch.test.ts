import { describe, expect, it } from "vitest";
import { parseSettingsWindowSnapshot } from "./settings-window-watch";

describe("parseSettingsWindowSnapshot", () => {
	it("reads a watcher line", () => {
		expect(parseSettingsWindowSnapshot('{"frontPid":1,"settingsPid":2,"bounds":{"x":590,"y":213,"width":740,"height":625}}'))
			.toEqual({ frontPid: 1, settingsPid: 2, bounds: { x: 590, y: 213, width: 740, height: 625 } });
	});

	it("treats missing or partial bounds as no window", () => {
		expect(parseSettingsWindowSnapshot('{"frontPid":1,"settingsPid":-1,"bounds":null}')?.bounds).toBeNull();
		expect(parseSettingsWindowSnapshot('{"frontPid":1,"settingsPid":2,"bounds":{"x":1}}')?.bounds).toBeNull();
	});

	it("ignores lines that are not snapshots", () => {
		expect(parseSettingsWindowSnapshot("execution error")).toBeUndefined();
		expect(parseSettingsWindowSnapshot('{"frontPid":"1"}')).toBeUndefined();
	});
});
