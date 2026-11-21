import { describe, expect, it } from "vitest";
import { primaryDecisionId } from "./approvalDecisions";

// The order Claude Code offers a Bash permission in.
const claude = [
	{ id: "reject", kind: "reject_once", label: "Deny" },
	{ id: "allow", kind: "allow_once", label: "Allow Once" },
	{ id: "allow_always", kind: "allow_always", label: "Always Allow" },
];

describe("primaryDecisionId", () => {
	// Desktop makes "Allow once" the accent button and Deny a secondary one; the
	// phone made whichever came first prominent, and Claude lists Deny first.
	it("makes Allow Once the primary even when Deny is listed first", () => {
		expect(primaryDecisionId(claude)).toBe("allow");
	});

	it("falls back to the first decision that isn't a rejection", () => {
		expect(primaryDecisionId([claude[0], claude[2]])).toBe("allow_always");
		expect(primaryDecisionId([{ id: "no", kind: "reject_always", label: "Never" }, { id: "go", kind: "something_new", label: "Go" }])).toBe("go");
	});

	it("keeps the first decision when every option is a rejection", () => {
		expect(primaryDecisionId([{ id: "a", kind: "reject_once", label: "Deny" }, { id: "b", kind: "reject_always", label: "Never" }])).toBe("a");
	});

	// A host that doesn't send kinds gives nothing to go on: keep its order.
	it("keeps the first decision when the host sends no kinds", () => {
		expect(primaryDecisionId([{ id: "deny", label: "Deny" }, { id: "allow", label: "Allow" }])).toBe("deny");
	});

	it("has no primary without decisions", () => {
		expect(primaryDecisionId([])).toBeUndefined();
		expect(primaryDecisionId(undefined)).toBeUndefined();
	});
});
