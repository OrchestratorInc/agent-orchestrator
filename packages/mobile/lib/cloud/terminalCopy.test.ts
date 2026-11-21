import { describe, expect, it } from "vitest";
import { cloudTerminalDisplay } from "./terminalCopy";

describe("cloud terminal copy", () => {
	it("says nothing while the terminal is live", () => {
		expect(cloudTerminalDisplay("open", undefined, "workspace")).toEqual({ state: "live" });
	});

	it("floats the transport's own progress note, or the local terminal's default", () => {
		expect(cloudTerminalDisplay("connecting", undefined, "agent")).toEqual({ state: "pending", label: "Connecting…" });
		expect(cloudTerminalDisplay("connecting", "Waiting for the sandbox to start…", "agent")).toEqual({ state: "pending", label: "Waiting for the sandbox to start…" });
		expect(cloudTerminalDisplay("connecting", "Reconnecting…", "workspace")).toEqual({ state: "pending", label: "Reconnecting…" });
	});

	it("offers a new shell when the workspace shell exits, and nothing to retry for an exited agent", () => {
		expect(cloudTerminalDisplay("exited", "The terminal has exited.", "workspace")).toMatchObject({ state: "ended", title: "Shell closed", action: "Open a new shell" });
		const agent = cloudTerminalDisplay("exited", "The terminal has exited.", "agent");
		expect(agent).toMatchObject({ state: "ended", title: "Agent terminal ended" });
		expect(agent).not.toHaveProperty("action");
	});

	it("explains a failure with the transport's reason and offers a retry", () => {
		expect(cloudTerminalDisplay("error", "This terminal is read-only.", "agent")).toEqual({
			state: "failed",
			title: "Terminal unavailable",
			message: "This terminal is read-only.",
			action: "Try again",
		});
		expect(cloudTerminalDisplay("error", undefined, "workspace")).toMatchObject({ state: "failed", message: "The connection to the sandbox was lost." });
	});
});
