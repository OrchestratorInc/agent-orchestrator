import { describe, expect, it } from "vitest";
import { WORKOS_REDIRECT_URI } from "./config";

describe("WORKOS_REDIRECT_URI", () => {
	// WorkOS rejects a redirect URI it has not been shown, and a generic scheme
	// can be claimed by any app on the device — this project's own dev build
	// already steals `aomobile://`. The auth redirect therefore uses the bundle
	// identifier, which only this app can register.
	it("uses the bundle-identifier scheme, not the generic app scheme", () => {
		expect(WORKOS_REDIRECT_URI).toBe("aoagents.ao://callback");
	});

	it("does not use the generic scheme that pairing links share", () => {
		expect(WORKOS_REDIRECT_URI.startsWith("aomobile://")).toBe(false);
	});

	it("is registered alongside the pairing scheme in Expo config", async () => {
		const appConfig = (await import("../../app.json")) as unknown as {
			default: { expo: { scheme: string | string[] } };
		};
		const registered = appConfig.default.expo.scheme;
		const schemes = Array.isArray(registered) ? registered : [registered];

		expect(schemes).toContain("aomobile");
		expect(schemes).toContain("aoagents.ao");
	});
});
