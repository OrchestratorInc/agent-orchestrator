import { describe, expect, it } from "vitest";
import { DEFAULT_DAEMON_SENTRY_DSN, daemonSentryDsn } from "./sentry-config";

describe("daemon Sentry handoff", () => {
	it("uses the daemon project for a packaged build", () => {
		expect(daemonSentryDsn(undefined, false)).toBe(DEFAULT_DAEMON_SENTRY_DSN);
	});

	it("leaves the DSN empty in a development build", () => {
		expect(daemonSentryDsn(undefined, true)).toBe("");
	});

	it("preserves explicit environment values, including an empty string", () => {
		expect(daemonSentryDsn("https://custom.example/1", false)).toBe("https://custom.example/1");
		expect(daemonSentryDsn("https://custom.example/1", true)).toBe("https://custom.example/1");
		expect(daemonSentryDsn("", false)).toBe("");
	});
});
