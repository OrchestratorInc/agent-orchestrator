import { CloudApiError } from "@aoagents/cloud-client";
import { describe, expect, it } from "vitest";
import { cloudRequestErrorCopy, isCloudSessionLimitError } from "./errorCopy";
describe("Cloud session limit rejection", () => {
	it("recognizes the structured session limit rejection and retains its request reference", () => {
		const error = new CloudApiError(409, { error: "conflict", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "quota-request" });
		expect(isCloudSessionLimitError(error)).toBe(true);
		expect(cloudRequestErrorCopy(error)).toContain("quota-request");
	});
	it.each([
		new CloudApiError(409, { error: "conflict", code: "IDEMPOTENCY_CONFLICT", message: "Concurrent session limit reached.", requestId: "r1" }),
		new CloudApiError(500, { error: "internal_error", code: "SANDBOX_QUOTA_EXCEEDED", message: "Concurrent session limit reached.", requestId: "r1" }),
		new Error("SANDBOX_QUOTA_EXCEEDED: Concurrent session limit reached."),
		{ status: 409, code: "SANDBOX_QUOTA_EXCEEDED" },
		null,
	])("does not infer a confirmed session rejection from a different error (%s)", (error) => {
		expect(isCloudSessionLimitError(error)).toBe(false);
	});
});
it.each([402, 409, 429])("preserves Cloud quota facts without inventing an Upgrade action (%s)", (status) => {
	const error = new CloudApiError(status, { error: "limit", code: "weekly_limit_reached", message: "Weekly compute limit reached.", requestId: "req-one", used: 12, limit: 12, resetsAt: "2026-10-14T00:00:00Z" }, 60);
	const message = cloudRequestErrorCopy(error);
	expect(message).toContain("Weekly compute limit reached."); expect(message).toContain("12 / 12");
	expect(message).toContain("2026-10-14T00:00:00Z"); expect(message).toContain("req-one"); expect(message).not.toContain("Upgrade");
});
it("does not reinterpret a Cloud auth rejection as a paired desktop password failure", () => {
	expect(cloudRequestErrorCopy(new CloudApiError(401, { error: "auth", code: "unauthorized", message: "Sign in again", requestId: "r1" }))).toContain("Sign in again");
});
it("can hide support references while preserving actionable limits and recovery facts", () => {
	const error = new CloudApiError(429, { error: "limit", code: "weekly_limit_reached", message: "Weekly compute limit reached.", requestId: "private-reference", used: 12, limit: 12 }, 60);
	const message = cloudRequestErrorCopy(error, { includeReference: false });
	expect(message).toContain("Weekly compute limit reached."); expect(message).toContain("12 / 12"); expect(message).toContain("60 seconds");
	expect(message).not.toContain("private-reference"); expect(message).not.toContain("Reference:"); expect(message).not.toContain("HTTP"); expect(message).not.toContain("weekly_limit_reached");
});
