import { describe, expect, it } from "vitest";
import { publishFailureReason } from "./artifact-telemetry";

describe("artifact publish telemetry", () => {
	it("names why a publish did not go through, never with its message", () => {
		expect(publishFailureReason({ status: "signed_out" })).toBe("signed_out");
		expect(publishFailureReason({ status: "cancelled" })).toBe("cancelled");
		expect(publishFailureReason({ status: "limit", message: "Free plan: up to 3 active links." })).toBe("refused");
		expect(publishFailureReason({ status: "unsupported", message: "Sharing videos is coming soon." })).toBe("refused");
		expect(publishFailureReason({ status: "not_owner" })).toBe("refused");
		expect(publishFailureReason({ status: "error", message: "Too big", failure: "too_large" })).toBe("too_large");
		expect(publishFailureReason({ status: "error", message: "Upload failed", failure: "upload_failed" })).toBe("upload_failed");
		expect(publishFailureReason({ status: "error", message: "Another file is being shared." })).toBe("other");
	});
});
