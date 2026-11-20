import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./bridge", () => ({ aoBridge: { telemetry: {} } }));

let visibility: DocumentVisibilityState = "hidden";

function setVisibility(next: DocumentVisibilityState): void {
	visibility = next;
	document.dispatchEvent(new Event("visibilitychange"));
}

async function loadLaunchTelemetry() {
	vi.resetModules();
	return import("./launch-telemetry");
}

describe("launch timings", () => {
	const start = { kind: "cold" as const, startedAtMs: 0 };

	beforeEach(() => {
		localStorage.clear();
		// Main creates the window hidden, so the page starts hidden.
		visibility = "hidden";
		vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
		start.startedAtMs = performance.timeOrigin + performance.now() - 1_200;
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("times a launch whose page started hidden before the window was shown", async () => {
		const launch = await loadLaunchTelemetry();
		setVisibility("visible");
		launch.markLaunchUsable();

		const properties = await launch.launchProperties(start, "1.2.3");
		expect(properties.launch_kind).toBe("cold");
		expect(properties.to_usable_ms).toBeGreaterThanOrEqual(1_200);
	});

	it("sends no timings when the window is hidden after it was shown", async () => {
		const launch = await loadLaunchTelemetry();
		setVisibility("visible");
		setVisibility("hidden");
		launch.markLaunchUsable();

		expect(await launch.launchProperties(start, "1.2.3")).toEqual({ launch_kind: "cold" });
	});

	it("keeps timings when the window is hidden only after it was usable", async () => {
		const launch = await loadLaunchTelemetry();
		setVisibility("visible");
		launch.markLaunchUsable();
		setVisibility("hidden");

		expect(await launch.launchProperties(start, "1.2.3")).toHaveProperty("to_usable_ms");
	});
});
