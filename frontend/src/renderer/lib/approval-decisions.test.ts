import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import { APPROVAL_DECISION_LABELS, approvalDecisionLabeler, orderedApprovalDecisions, parseApprovalDecisions } from "./approval-decisions";

describe("approval decisions", () => {
	it("orders deny first, then allow once, then broader allows, keeping the provider's order otherwise", () => {
		const ordered = orderedApprovalDecisions([
			{ id: "always", label: "Always", kind: "allow_always" },
			{ id: "other", label: "Something else" },
			{ id: "once", label: "Allow", kind: "allow_once" },
			{ id: "no", label: "Reject", kind: "reject_once" },
		]);
		expect(ordered.map((decision) => decision.id)).toEqual(["no", "once", "always", "other"]);
	});

	it("keeps the provider's words for a kind offered more than once", () => {
		const decisions = [
			{ id: "a", label: "Use Postgres", kind: "allow_once" as const },
			{ id: "b", label: "Use SQLite", kind: "allow_once" as const },
			{ id: "no", label: "Reject", kind: "reject_once" as const },
		];
		const label = approvalDecisionLabeler(decisions);
		expect(decisions.map(label)).toEqual(["Use Postgres", "Use SQLite", "Deny"]);
		expect(approvalDecisionLabeler([{ id: "x", label: "Allow", kind: "allow_always" }], "file_change")({ id: "x", label: "Allow", kind: "allow_always" }))
			.toBe("Always allow these file changes");
	});

	it("says the same thing in the bell as in the chat", () => {
		expect(en["notify.approval.allowOnce"]).toBe(APPROVAL_DECISION_LABELS.allowOnce);
		expect(en["notify.approval.allowAlwaysCommand"]).toBe(APPROVAL_DECISION_LABELS.allowAlwaysCommand);
		expect(en["notify.approval.allowAlwaysFiles"]).toBe(APPROVAL_DECISION_LABELS.allowAlwaysFiles);
		expect(en["notify.approval.deny"]).toBe(APPROVAL_DECISION_LABELS.deny);
		expect(en["notify.approval.alwaysDeny"]).toBe(APPROVAL_DECISION_LABELS.alwaysDeny);
	});

	it("reads decisions from a notification's metadata, dropping anything malformed", () => {
		expect(parseApprovalDecisions([
			{ id: "once", label: "Allow", kind: "allow_once" },
			{ id: "odd", label: "Maybe", kind: "sometimes" },
			{ id: 3, label: "Bad" },
			null,
		])).toEqual([{ id: "once", label: "Allow", kind: "allow_once" }, { id: "odd", label: "Maybe" }]);
		expect(parseApprovalDecisions(undefined)).toEqual([]);
	});
});
