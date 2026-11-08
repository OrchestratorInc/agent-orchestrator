import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn());
vi.mock("./telemetry", () => ({ captureRendererEvent: capture }));

const { useUiStore } = await import("../stores/ui-store");
const { captureTopbarAction } = await import("./topbar-telemetry");

describe("captureTopbarAction", () => {
	beforeEach(() => capture.mockClear());

	it("reports a click from the simplified top bar", () => {
		useUiStore.setState({ developerMode: true });
		captureTopbarAction("run_cue", "worker");
		expect(capture).toHaveBeenCalledWith("ao.renderer.topbar_action_clicked", { action: "run_cue", view: "worker" });
	});

	it("omits the view when there is none", () => {
		useUiStore.setState({ developerMode: true });
		captureTopbarAction("notifications");
		expect(capture).toHaveBeenCalledWith("ao.renderer.topbar_action_clicked", { action: "notifications" });
	});

	it("stays silent on the previous top bar", () => {
		useUiStore.setState({ developerMode: false });
		captureTopbarAction("new_task", "orchestrator");
		expect(capture).not.toHaveBeenCalled();
	});
});
