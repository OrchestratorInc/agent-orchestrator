import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn());
vi.mock("./telemetry", () => ({ captureRendererEvent: capture }));

const { captureTopbarAction } = await import("./topbar-telemetry");

describe("captureTopbarAction", () => {
	beforeEach(() => capture.mockClear());

	it("reports a click from the top bar", () => {
		captureTopbarAction("run_cue", "worker");
		expect(capture).toHaveBeenCalledWith("ao.renderer.topbar_action_clicked", { action: "run_cue", view: "worker" });
	});

	it("omits the view when there is none", () => {
		captureTopbarAction("notifications");
		expect(capture).toHaveBeenCalledWith("ao.renderer.topbar_action_clicked", { action: "notifications" });
	});
});
