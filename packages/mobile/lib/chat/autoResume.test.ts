import { describe, expect, it } from "vitest";
import { shouldAutoResume } from "./autoResume";

describe("shouldAutoResume", () => {
	it("resumes an exited worker", () => {
		expect(shouldAutoResume({ status: "exited", provisionState: "ready" }, false, true)).toBe(true);
	});

	// An orchestrator link carries no provisionState; it must still resume.
	it("resumes an exited orchestrator", () => {
		expect(shouldAutoResume({ status: "exited" }, false, true)).toBe(true);
	});

	it("leaves terminated, failed-provision, running and unpaired sessions alone", () => {
		expect(shouldAutoResume({ status: "exited" }, true, true)).toBe(false);
		expect(shouldAutoResume({ status: "exited", provisionState: "failed" }, false, true)).toBe(false);
		expect(shouldAutoResume({ status: "working" }, false, true)).toBe(false);
		expect(shouldAutoResume({ status: "exited" }, false, false)).toBe(false);
	});
});
