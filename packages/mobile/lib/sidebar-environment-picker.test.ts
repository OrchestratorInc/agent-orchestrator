import { describe, expect, it } from "vitest";
import { sidebarEnvironmentOptions } from "./sidebar-navigation";

describe("sidebar environment picker", () => {
	it("marks the active environment without hiding the other destination", () => {
		expect(sidebarEnvironmentOptions("cloud")).toEqual([
			{ id: "local", label: "Local", selected: false },
			{ id: "cloud", label: "Cloud", selected: true },
		]);
	});

	it("leaves both choices unselected while persisted state is loading", () => {
		expect(sidebarEnvironmentOptions(null)).toEqual([
			{ id: "local", label: "Local", selected: false },
			{ id: "cloud", label: "Cloud", selected: false },
		]);
	});
});
