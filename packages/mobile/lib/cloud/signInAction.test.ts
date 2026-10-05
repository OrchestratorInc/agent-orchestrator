import { describe, expect, it, vi } from "vitest";
import { beginCloudSignIn } from "./signInAction";

describe("beginCloudSignIn", () => {
	it("opens the native credential sheet only for a local development control plane", async () => {
		const openLocalAuth = vi.fn();
		const signInWithWorkOS = vi.fn(async () => true);
		const result = await beginCloudSignIn({ localAuth: true, openLocalAuth, signInWithWorkOS });

		expect(result).toEqual({ status: "local" });
		expect(openLocalAuth).toHaveBeenCalledOnce();
		expect(signInWithWorkOS).not.toHaveBeenCalled();
	});

	it("authenticates directly through hosted AuthKit in production", async () => {
		const result = await beginCloudSignIn({
			localAuth: false,
			openLocalAuth: vi.fn(),
			signInWithWorkOS: async () => true,
		});

		expect(result).toEqual({ status: "authenticated" });
	});

	it("does not treat a dismissed browser session as authentication", async () => {
		const result = await beginCloudSignIn({
			localAuth: false,
			openLocalAuth: vi.fn(),
			signInWithWorkOS: async () => false,
		});

		expect(result).toEqual({ status: "cancelled" });
	});

	it("returns a user-facing failure when hosted authentication throws", async () => {
		const result = await beginCloudSignIn({
			localAuth: false,
			openLocalAuth: vi.fn(),
			signInWithWorkOS: async () => { throw new Error("Browser unavailable"); },
		});

		expect(result).toEqual({ status: "failed", message: "Browser unavailable" });
	});
});
