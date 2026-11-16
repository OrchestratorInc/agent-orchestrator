import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_DAEMON_SENTRY_DSN, daemonSentryDsn, isPublishedSentryBuild } from "./sentry-config";

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
});
