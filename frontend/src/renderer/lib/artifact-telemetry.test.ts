import { describe, expect, it } from "vitest";
import { publishResultEvent } from "./artifact-telemetry";

describe("artifact publish telemetry", () => {
	it("names a publish attempt's result, never with its message", () => {
		const share = { id: "s1", name: "report.html", url: "https://example.test/s/1" } as never;
		expect(publishResultEvent({ status: "published", share, replaced: false }, false)).toEqual(["ao.artifacts.published", { is_republish: false }]);
		expect(publishResultEvent({ status: "signed_out" }, true)).toEqual(["ao.artifacts.publish_failed", { is_republish: true, reason: "signed_out" }]);
		expect(publishResultEvent({ status: "limit", message: "Free plan: up to 3 active links." }, false)[1].reason).toBe("refused");
		expect(publishResultEvent({ status: "unsupported", message: "Sharing videos is coming soon." }, false)[1].reason).toBe("refused");
		expect(publishResultEvent({ status: "not_owner" }, false)[1].reason).toBe("refused");
		expect(publishResultEvent({ status: "error", message: "Too big", failure: "too_large" }, false)[1].reason).toBe("too_large");
		expect(publishResultEvent({ status: "error", message: "Upload failed", failure: "upload_failed" }, false)[1].reason).toBe("upload_failed");
		expect(publishResultEvent({ status: "error", message: "Another file is being shared." }, false)[1].reason).toBe("other");
	});

	it("counts a cancelled upload as cancelled, not as a failure", () => {
		expect(publishResultEvent({ status: "cancelled" }, true)).toEqual(["ao.artifacts.publish_cancelled", { is_republish: true }]);
	});
});
