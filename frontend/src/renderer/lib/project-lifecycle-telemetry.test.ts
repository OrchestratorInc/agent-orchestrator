import { describe, expect, it, vi } from "vitest";
import { captureProjectAddFailed } from "./project-lifecycle-telemetry";
import { sanitizeRendererProperties } from "./telemetry";

describe("project add telemetry", () => {
	it("fires project_add_failed with the error code", async () => {
		const capture = vi.fn();
		captureProjectAddFailed(capture, "PATH_ALREADY_REGISTERED");
		expect(capture).toHaveBeenCalledWith("ao.projects.add_failed", { error_code: "PATH_ALREADY_REGISTERED" });
		expect(await sanitizeRendererProperties("ao.projects.add_failed", { error_code: "PATH_ALREADY_REGISTERED" })).toEqual({
			error_code: "PATH_ALREADY_REGISTERED",
		});
		expect(await sanitizeRendererProperties("ao.projects.clone_failed", { error_code: "unknown" })).toEqual({
			error_code: "unknown",
		});
		expect(await sanitizeRendererProperties("ao.projects.add_failed", { error_code: "not a code", path: "/tmp/x" })).toEqual({});
		captureProjectAddFailed(capture, undefined);
		expect(capture).toHaveBeenLastCalledWith("ao.projects.add_failed", { error_code: "unknown" });
	});
});
