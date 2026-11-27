import { describe, expect, it } from "vitest";
import { buildMobileContext, MOBILE_TELEMETRY_SCHEMA_VERSION } from "./context";

describe("buildMobileContext", () => {
	it("tags the native app as client=mobile with the OS platform", () => {
		const ctx = buildMobileContext({ platformOS: "ios", isPhysicalDevice: true, dev: false, appVersion: "1.1.0" });
		expect(ctx.client).toBe("mobile");
		expect(ctx.platform).toBe("ios");
		expect(ctx.build_mode).toBe("device");
		expect(ctx.app_version).toBe("1.1.0");
		expect(ctx.telemetry_schema_version).toBe(MOBILE_TELEMETRY_SCHEMA_VERSION);
	});

	// The bifurcation the ask named: the web build must not count as a native app.
	it("tags the web build as client=mobile-web", () => {
		const ctx = buildMobileContext({ platformOS: "web", isPhysicalDevice: false, dev: false, appVersion: "1.1.0" });
		expect(ctx.client).toBe("mobile-web");
		expect(ctx.platform).toBe("web");
	});

	it("distinguishes dev, simulator, and device builds", () => {
		expect(buildMobileContext({ platformOS: "android", isPhysicalDevice: true, dev: true, appVersion: "x" }).build_mode).toBe("dev");
		expect(buildMobileContext({ platformOS: "android", isPhysicalDevice: false, dev: false, appVersion: "x" }).build_mode).toBe("simulator");
		expect(buildMobileContext({ platformOS: "android", isPhysicalDevice: true, dev: false, appVersion: "x" }).build_mode).toBe("device");
	});

	it("falls back to platform=other and version=unknown for junk input", () => {
		const ctx = buildMobileContext({ platformOS: "windows", isPhysicalDevice: true, dev: false, appVersion: "   " });
		expect(ctx.platform).toBe("other");
		expect(ctx.app_version).toBe("unknown");
	});

	it("tags the running OTA update and its channel", () => {
		const id = "0198A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B";
		const ctx = buildMobileContext({ platformOS: "ios", isPhysicalDevice: true, dev: false, appVersion: "2.4.0", update: { id, embedded: false, channel: "production" } });
		expect(ctx.update_id).toBe(id.toLowerCase());
		expect(ctx.update_channel).toBe("production");
	});

	it("reports the bundle shipped in the binary as embedded", () => {
		const ctx = buildMobileContext({ platformOS: "android", isPhysicalDevice: true, dev: false, appVersion: "2.4.0", update: { id: "0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", embedded: true, channel: "preview" } });
		expect(ctx.update_id).toBe("embedded");
		expect(ctx.update_channel).toBe("preview");
	});

	it("never forwards a malformed update id or an unknown channel", () => {
		const ctx = buildMobileContext({ platformOS: "ios", isPhysicalDevice: true, dev: false, appVersion: "2.4.0", update: { id: "not-a-uuid /Users/x", embedded: false, channel: "my-branch" } });
		expect(ctx.update_id).toBeUndefined();
		expect(ctx.update_channel).toBe("other");
	});

	it("leaves the update fields out where updates are off", () => {
		const ctx = buildMobileContext({ platformOS: "ios", isPhysicalDevice: false, dev: true, appVersion: "2.4.0" });
		expect(ctx).not.toHaveProperty("update_id");
		expect(ctx).not.toHaveProperty("update_channel");
	});
});
