import { describe, expect, it } from "vitest";
import { canRepairWithQr } from "./machineRecovery";

describe("QR machine recovery", () => {
	it.each(["unreachable", "tunnel-rotated", "auth"] as const)("offers a fresh scan for %s", (failure) => {
		expect(canRepairWithQr(failure)).toBe(true);
	});

	it.each([null, "rate-limited", "server-error", "incompatible-host"] as const)("does not mislabel %s as a QR-repairable failure", (failure) => {
		expect(canRepairWithQr(failure)).toBe(false);
	});
});
