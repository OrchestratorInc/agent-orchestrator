import { CloudApiError } from "@aoagents/cloud-client";
import { describe, expect, it } from "vitest";
import { cloudRequestErrorCopy } from "./errorCopy";
it.each([402, 409, 429])("preserves Cloud quota facts without inventing an Upgrade action (%s)", (status) => {
	const error = new CloudApiError(status, { error: "limit", code: "weekly_limit_reached", message: "Weekly compute limit reached.", requestId: "req-one", used: 12, limit: 12, resetsAt: "2026-10-14T00:00:00Z" }, 60);
	const message = cloudRequestErrorCopy(error);
	expect(message).toContain("Weekly compute limit reached."); expect(message).toContain("12 / 12");
	expect(message).toContain("2026-10-14T00:00:00Z"); expect(message).toContain("req-one"); expect(message).not.toContain("Upgrade");
});
it("does not reinterpret a Cloud auth rejection as a paired desktop password failure", () => {
	expect(cloudRequestErrorCopy(new CloudApiError(401, { error: "auth", code: "unauthorized", message: "Sign in again", requestId: "r1" }))).toContain("Sign in again");
});
