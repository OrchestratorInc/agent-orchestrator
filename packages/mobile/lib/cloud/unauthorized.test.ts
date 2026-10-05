import { CloudApiError } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { wrapUnauthorized } from "./unauthorized";

function fakeClient(methods: Record<string, (...args: unknown[]) => unknown>) {
	// wrapUnauthorized only calls Reflect.get/apply on whatever it's given, so
	// a plain object stands in for the real CloudClient class.
	return methods as unknown as import("@aoagents/cloud-client").CloudClient;
}

describe("wrapUnauthorized", () => {
	it("calls onUnauthorized and rethrows on a 401", async () => {
		const onUnauthorized = vi.fn();
		const error = new CloudApiError(401, { error: "Unauthorized", code: "AUTH_REQUIRED", message: "nope", requestId: "" });
		const client = wrapUnauthorized(
			fakeClient({ getCurrentAccount: async () => { throw error; } }),
			onUnauthorized,
			() => 0,
		);

		await expect(client.getCurrentAccount()).rejects.toBe(error);
		expect(onUnauthorized).toHaveBeenCalledTimes(1);
	});

	it("does not call onUnauthorized on a non-401 error", async () => {
		const onUnauthorized = vi.fn();
		const error = new CloudApiError(500, { error: "Server Error", code: "INTERNAL", message: "boom", requestId: "" });
		const client = wrapUnauthorized(
			fakeClient({ getCurrentAccount: async () => { throw error; } }),
			onUnauthorized,
			() => 0,
		);

		await expect(client.getCurrentAccount()).rejects.toBe(error);
		expect(onUnauthorized).not.toHaveBeenCalled();
	});

	it("passes through a successful call untouched", async () => {
		const onUnauthorized = vi.fn();
		const client = wrapUnauthorized(
			fakeClient({ getCurrentAccount: async () => ({ id: "acct" }) }),
			onUnauthorized,
			() => 0,
		);

		await expect(client.getCurrentAccount()).resolves.toEqual({ id: "acct" });
		expect(onUnauthorized).not.toHaveBeenCalled();
	});
});
