import { beforeEach, describe, expect, it, vi } from "vitest";

const alert = vi.hoisted(() => vi.fn());
vi.mock("react-native", () => ({ Alert: { alert } }));

import { terminalInterfaceFailureRecovery } from "./terminalInterfaceRecovery";

describe("terminalInterfaceFailureRecovery", () => {
	beforeEach(() => alert.mockReset());

	it("offers an explicit destructive cancellation when a provider decision blocks drain", () => {
		const recovery = terminalInterfaceFailureRecovery({ errorCode: "DRAIN_DECISION_PENDING" });

		expect(recovery).toMatchObject({
			actionLabel: "Cancel request and switch",
			policy: "interrupt",
			confirmStyle: "destructive",
			confirmationAction: "Cancel request and switch",
		});
		expect(recovery?.confirmationTitle).toMatch(/cancel request/i);
		expect(recovery?.confirmationMessage).toMatch(/pending provider request/i);

		const start = vi.fn();
		recovery?.confirm(start);
		const buttons = alert.mock.calls[0]?.[2];
		expect(buttons?.[0]).toMatchObject({ text: "Keep request", style: "cancel" });
		buttons?.[1]?.onPress?.();
		expect(start).toHaveBeenCalledWith("interrupt");
	});

	it("does not advertise a destructive retry for unrelated transition failures", () => {
		expect(terminalInterfaceFailureRecovery({ errorCode: "DRAIN_QUIESCENCE_UNVERIFIED" })).toBeUndefined();
		expect(terminalInterfaceFailureRecovery({ errorCode: "TARGET_RESUME_FAILED" })).toBeUndefined();
		expect(terminalInterfaceFailureRecovery(undefined)).toBeUndefined();
	});
});
