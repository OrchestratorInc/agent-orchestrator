import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_DAEMON_SENTRY_DSN, daemonSentryDsn, isPublishedSentryBuild, safeSentryExceptionType, sentryEnvironment } from "./sentry-config";

import { sentryExceptionValue } from "../../../packages/shared/telemetry/sentry-message";

beforeEach(() => { vi.stubEnv("AO_PUBLISHED_BUILD", "1"); });
afterEach(() => { vi.unstubAllEnvs(); });

describe("daemon Sentry handoff", () => {
	it("uses the daemon project for a packaged build", () => {
		expect(daemonSentryDsn(undefined, "0.14.10", false)).toBe(DEFAULT_DAEMON_SENTRY_DSN);
	});

	it("leaves the DSN empty in a development build", () => {
		expect(daemonSentryDsn(undefined, "0.14.10", true)).toBe("");
	});

	it("leaves the DSN empty in a copy that is not a published build", () => {
		expect(daemonSentryDsn(undefined, "0.13.0-ragcorp.7", false)).toBe("");
		vi.stubEnv("AO_PUBLISHED_BUILD", "");
		expect(daemonSentryDsn(undefined, "0.14.10", false)).toBe("");
	});

	it("preserves explicit environment values, including an empty string", () => {
		expect(daemonSentryDsn("https://custom.example/1", "0.14.10", false)).toBe("https://custom.example/1");
		expect(daemonSentryDsn("https://custom.example/1", "development", true)).toBe("https://custom.example/1");
		expect(daemonSentryDsn("", "0.14.10", false)).toBe("");
	});
});

describe("published Sentry builds", () => {
	it("accepts only release-stamped stable and nightly versions", () => {
		for (const version of ["0.14.10", "0.14.11-nightly.202611100625"]) expect(isPublishedSentryBuild(version, false)).toBe(true);
		// Every one of these reached Sentry from a self-built or development copy.
		for (const version of ["development", "", "0.13.0-ragcorp.7", "0.14.10+ptbr.1", "0.14.9-nightly.202610311751.local", "0.14.10-custom.1", "0.13.4-noetaxis.4612024", "0.14.11-edge.1"]) {
			expect(isPublishedSentryBuild(version, false)).toBe(false);
		}
		expect(isPublishedSentryBuild("0.14.10", true)).toBe(false);
	});

	it("requires the release build marker even for a stable-looking version", () => {
		vi.stubEnv("AO_PUBLISHED_BUILD", "");
		expect(isPublishedSentryBuild("0.13.0", false)).toBe(false);
	});

	it("accepts release candidates alongside stable and nightly versions", () => {
		for (const version of ["0.15.8", "0.15.8-nightly.202611281200", "0.15.8-rc.1", "0.15.8-rc.12"]) {
			expect(isPublishedSentryBuild(version, false)).toBe(true);
		}
		expect(daemonSentryDsn(undefined, "0.15.8-rc.1", false)).toBe(DEFAULT_DAEMON_SENTRY_DSN);
	});

	it("rejects malformed release candidates and other suffixes", () => {
		for (const version of ["0.15.8-rc", "0.15.8-rc.x", "0.15.8-rc.1.202611281200", "0.15.8-pr123.1", "0.15.8+fork.1"]) {
			expect(isPublishedSentryBuild(version, false)).toBe(false);
		}
	});

	it("gives a release candidate no DSN without the release marker or in dev", () => {
		expect(isPublishedSentryBuild("0.15.8-rc.1", true)).toBe(false);
		expect(daemonSentryDsn(undefined, "0.15.8-rc.1", true)).toBe("");
		vi.stubEnv("AO_PUBLISHED_BUILD", "");
		expect(isPublishedSentryBuild("0.15.8-rc.1", false)).toBe(false);
		expect(daemonSentryDsn(undefined, "0.15.8-rc.1", false)).toBe("");
	});
});

describe("Sentry environment", () => {
	it("reports a release candidate as stable", () => {
		expect(sentryEnvironment("0.15.8")).toBe("stable");
		expect(sentryEnvironment("0.15.8-rc.1")).toBe("stable");
		expect(sentryEnvironment("0.15.8-rc.12")).toBe("stable");
		expect(sentryEnvironment("0.15.8-nightly.202611281200")).toBe("nightly");
		expect(sentryEnvironment("0.15.8-pr123.1")).toBe("development");
		expect(sentryEnvironment("")).toBe("unknown");
	});
});

describe("Sentry exception fields", () => {
	it("keeps registered names and rejects arbitrary custom names", () => {
		expect(safeSentryExceptionType("WorkspaceRequestError")).toBe("WorkspaceRequestError");
		expect(safeSentryExceptionType("PrivateProjectError")).toBe("Error");
	});

	it("caps the complete exception value including its location", () => {
		expect(sentryExceptionValue("Invalid array length", "Fallback", "x".repeat(180), "render_crash").length).toBe(200);
		expect(sentryExceptionValue("", "Fallback", "unknown", "other")).toBe("Fallback");
	});
});
