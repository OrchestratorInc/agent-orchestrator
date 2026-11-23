import { describe, expect, it } from "vitest";
import { approvalPushAction, BROWSER_APPROVAL_CATEGORY, browserApprovalCategoryActions } from "./approvalPush";
import { notificationTarget } from "../notificationView";

const data = { type: "browser_approval", hostId: "h_a", sessionId: "s 1", approvalId: "apr_1" };

describe("browser approval push", () => {
	it("push tap opens preview with the card", () => {
		expect(notificationTarget(data, "h_a")).toBe("/preview/s%201?hostId=h_a&approval=apr_1");
		expect(approvalPushAction(data, "expo.modules.notifications.actions.DEFAULT")).toEqual({ kind: "open" });
	});

	it("Deny acts from the notification without opening the app", () => {
		expect(approvalPushAction(data, "deny")).toEqual({ kind: "deny", sessionId: "s 1", approvalId: "apr_1" });
	});

	it("Allow only opens the card: the decision is made unlocked, in the app", () => {
		expect(approvalPushAction(data, "allow")).toEqual({ kind: "open" });
		const [deny, allow] = browserApprovalCategoryActions;
		expect(BROWSER_APPROVAL_CATEGORY).toBe("browser_approval");
		expect(deny).toEqual(expect.objectContaining({ identifier: "deny", options: expect.objectContaining({ opensAppToForeground: false, isDestructive: true }) }));
		expect(allow).toEqual(expect.objectContaining({ identifier: "allow", options: expect.objectContaining({ opensAppToForeground: true, isAuthenticationRequired: true }) }));
	});

	it("ignores other notifications and an approval push without its id", () => {
		expect(approvalPushAction({ type: "needs_input" }, "deny")).toBeNull();
		expect(approvalPushAction({ ...data, approvalId: undefined }, "deny")).toEqual({ kind: "open" });
	});

	it("a Cloud approval opens its session with the card", () => {
		expect(approvalPushAction({ ...data, source: "cloud", orgId: "o1" }, "deny")).toEqual({ kind: "deny", sessionId: "s 1", approvalId: "apr_1", orgId: "o1" });
	});
});
