import { describe, expect, it, vi } from "vitest";
import { captureProjectAddFailed } from "./project-lifecycle-telemetry";

describe("project add telemetry", () => {
	it("fires project_add_failed with the error code", () => {
		const capture = vi.fn();
		captureProjectAddFailed(capture, "PATH_ALREADY_REGISTERED");
		expect(capture).toHaveBeenCalledWith("ao.renderer.project_add_failed", { error_code: "PATH_ALREADY_REGISTERED" });
		captureProjectAddFailed(capture, undefined);
		expect(capture).toHaveBeenLastCalledWith("ao.renderer.project_add_failed", { error_code: "unknown" });
	});
});
