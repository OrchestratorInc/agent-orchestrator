import { describe, expect, it } from "vitest";
import { CLOUD_DEPLOYMENT, WORKOS_CLIENT_ID, WORKOS_REDIRECT_URI } from "./config";
import { buildWorkOSAuthUrl } from "./signIn";

describe("configured WorkOS return", () => {
	it("sends the configured authorization redirect, not the native bounce, to WorkOS", () => {
		const url = new URL(buildWorkOSAuthUrl({ clientId: WORKOS_CLIENT_ID, redirectUri: WORKOS_REDIRECT_URI, codeChallenge: "challenge", state: "state" }));
		expect(url.searchParams.get("redirect_uri")).toBe(CLOUD_DEPLOYMENT.baseUrl === "https://api.aoagents.dev" ? "https://api.aoagents.dev/app/auth/return" : "aoagents.ao://callback");
	});

	it("does not listen for the generic scheme that pairing links share", () => {
		expect(CLOUD_DEPLOYMENT.callbackUri.startsWith("aomobile://")).toBe(false);
	});

	it("is registered alongside the pairing scheme in Expo config", async () => {
		const appConfig = (await import("../../app.json")) as unknown as {
			default: { expo: { scheme: string | string[] } };
		};
		const registered = appConfig.default.expo.scheme;
		const schemes = Array.isArray(registered) ? registered : [registered];

		expect(schemes).toContain("aomobile");
		expect(schemes).toContain(new URL(CLOUD_DEPLOYMENT.callbackUri).protocol.replace(":", ""));
	});
});
