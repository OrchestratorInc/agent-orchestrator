import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_ELECTRON_SENTRY_DSN } from "../shared/sentry-config";
import { electronMainSentryDsn, initMainSentry, sanitizeRendererCapture } from "./sentry-main";

afterEach(() => vi.unstubAllEnvs());

describe("Electron main Sentry configuration", () => {
	it("selects the Electron project unless AO_SENTRY_DSN overrides it", () => {
		vi.stubEnv("AO_SENTRY_DSN", undefined);
		expect(electronMainSentryDsn()).toBe(DEFAULT_ELECTRON_SENTRY_DSN);
		vi.stubEnv("AO_SENTRY_DSN", "https://custom.example/2");
		expect(electronMainSentryDsn()).toBe("https://custom.example/2");
		vi.stubEnv("AO_SENTRY_DSN", "");
		expect(electronMainSentryDsn()).toBe("");
	});

	it("keeps the Electron sender inactive", async () => {
		expect(await initMainSentry("1.0.0", "/tmp/ao-test")).toBeNull();
	});
});

describe("sanitizeRendererCapture", () => {
	it("scrubs local paths and loopback URLs at the Electron-main boundary", () => {
		const input = {
			consentGeneration: "generation-1",
			kind: "exception",
			level: "error",
			message: "failed /Users/alice/project/file.ts at http://127.0.0.1:3001/api and C:\\Users\\alice\\repo\\file.ts",
			tags: {
				operation: "GET http://localhost:3001/api/v1/sessions",
				category: "file:///home/alice/private.txt",
			},
		} as const;

		const sanitized = sanitizeRendererCapture(input);

		expect(sanitized).toEqual({
			consentGeneration: "generation-1",
			kind: "exception",
			level: "error",
			message: "failed [redacted-path] at [redacted-url] and [redacted-path]",
			tags: {
				operation: "GET [redacted-url]",
				category: "[redacted-url]",
			},
		});
		expect(input.message).toContain("/Users/alice");
	});

	it("rejects unknown fields, tags, kinds, and levels", () => {
		const base = { consentGeneration: "generation-1", kind: "message", message: "safe" };
		expect(sanitizeRendererCapture({ ...base, rawError: "secret" })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, tags: { request_id: "identifier" } })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, kind: "trace" })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, level: "debug" })).toBeNull();
	});
});
