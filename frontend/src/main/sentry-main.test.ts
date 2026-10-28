import { afterEach, describe, expect, it, vi } from "vitest";
import type { ElectronMainOptions, ErrorEvent } from "@sentry/electron/main";
import { DEFAULT_ELECTRON_SENTRY_DSN } from "../shared/sentry-config";
import { electronMainSentryDsn, initMainSentry, sanitizeRendererCapture } from "./sentry-main";

const sdk = vi.hoisted(() => ({
	init: vi.fn<(options: ElectronMainOptions) => void>(),
	captureEvent: vi.fn<(event: ErrorEvent) => void>(),
	close: vi.fn().mockResolvedValue(true),
	setUser: vi.fn(),
	clearCurrent: vi.fn(),
	clearIsolation: vi.fn(),
	makeElectronTransport: vi.fn(),
	onUncaughtExceptionIntegration: vi.fn(() => ({ name: "OnUncaughtException" })),
	onUnhandledRejectionIntegration: vi.fn(() => ({ name: "OnUnhandledRejection" })),
	childProcessIntegration: vi.fn(() => ({ name: "ChildProcess" })),
}));
vi.mock("@sentry/electron/main", () => ({
	...sdk,
	getCurrentScope: () => ({ clear: sdk.clearCurrent }),
	getIsolationScope: () => ({ clear: sdk.clearIsolation }),
}));
vi.mock("../shared/telemetry", () => ({ loadOrCreateTelemetryInstallId: vi.fn().mockResolvedValue("ins_test") }));

afterEach(() => { vi.unstubAllEnvs(); vi.clearAllMocks(); sdk.close.mockResolvedValue(true); });

describe("Electron main Sentry configuration", () => {
	it("selects the Electron project unless AO_SENTRY_DSN overrides it", () => {
		vi.stubEnv("AO_SENTRY_DSN", undefined);
		expect(electronMainSentryDsn()).toBe(DEFAULT_ELECTRON_SENTRY_DSN);
		vi.stubEnv("AO_SENTRY_DSN", "https://custom.example/2");
		expect(electronMainSentryDsn()).toBe("https://custom.example/2");
		vi.stubEnv("AO_SENTRY_DSN", "");
		expect(electronMainSentryDsn()).toBe("");
	});

	it("creates an online client with consent-owned crash integrations and an install user", async () => {
		vi.stubEnv("AO_SENTRY_DSN", undefined);
		const transport = await initMainSentry("1.0.0", "/tmp/ao-test", true);
		expect(transport).toBeTruthy();
		expect(sdk.init).toHaveBeenCalledOnce();
		const options = sdk.init.mock.calls[0]?.[0];
		expect(options).toMatchObject({
			dsn: DEFAULT_ELECTRON_SENTRY_DSN,
			transport: sdk.makeElectronTransport,
			ipcMode: 0,
			defaultIntegrations: [],
			maxBreadcrumbs: 0,
			sendDefaultPii: false,
		});
		expect(Array.isArray(options.integrations) ? options.integrations.map((item) => item.name) : undefined).toEqual(["OnUncaughtException", "OnUnhandledRejection", "ChildProcess"]);
		expect(sdk.childProcessIntegration).toHaveBeenCalledWith({ breadcrumbs: false, events: ["abnormal-exit", "crashed", "oom", "launch-failed", "integrity-failure"] });
		expect(sdk.setUser).toHaveBeenCalledWith({ id: "ins_test" });
	});

	it("forwards a safe renderer event and drains before clearing SDK scopes", async () => {
		const transport = await initMainSentry("1.0.0", "/tmp/ao-test", true);
		const request = { consentGeneration: "generation-1", kind: "exception", message: "get project ai-sports-coach-113 failed", level: "error", tags: { category: "http_5xx", code: "WORKSPACE_GIT_READ_FAILED" } } as const;
		transport.capture(request);
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		expect(JSON.stringify(sdk.captureEvent.mock.calls[0])).not.toContain("ai-sports-coach-113");
		expect(sdk.captureEvent.mock.calls[0]?.[0]).toMatchObject({ message: "Renderer error", tags: { "ao.origin": "renderer", "ao.category": "http_5xx", "ao.code": "WORKSPACE_GIT_READ_FAILED" } });
		transport.capture({ ...request, kind: "breadcrumb" });
		transport.capture({ ...request, tags: { category: "daemon_unavailable" } });
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		await transport.closeAndDrain();
		expect(sdk.close).toHaveBeenCalledWith(2_000);
		transport.capture(request);
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		await transport.clearCache();
		expect(sdk.clearCurrent).toHaveBeenCalledOnce();
		expect(sdk.clearIsolation).toHaveBeenCalledOnce();
		await initMainSentry("1.0.0", "/tmp/ao-test", true);
		expect(Array.isArray(sdk.init.mock.calls[1]?.[0].integrations) ? sdk.init.mock.calls[1]?.[0].integrations.map((item) => item.name) : undefined).toEqual(["OnUncaughtException", "OnUnhandledRejection"]);
	});

	it("drops dynamic exception text, paths, account fields, and breadcrumbs before send", async () => {
		await initMainSentry("1.0.0", "/tmp/ao-test", true);
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const result = beforeSend({
			type: undefined,
			message: "resume agent ai-sports-coach-113",
			server_name: "private-host",
			request: { url: "file:///Users/alice/private-repo" },
			user: { id: "spoofed", email: "private@example.test" },
			breadcrumbs: [{ message: "private branch" }],
			tags: { "ao.origin": "renderer", "ao.category": "http_5xx", "ao.code": "WORKSPACE_GIT_READ_FAILED", branch: "private" },
			exception: { values: [{ type: "Error", value: "get project ai-sports-coach-113", stacktrace: { frames: [{ function: "loadProject", filename: "/Users/alice/private-repo/file.ts", context_line: "private" }] } }] },
		});
		expect(result).toMatchObject({ message: "Renderer error", user: { id: "ins_test" }, fingerprint: ["electron", "renderer", "http_5xx", "WORKSPACE_GIT_READ_FAILED"] });
		const encoded = JSON.stringify(result);
		for (const secret of ["ai-sports-coach-113", "private-repo", "private branch", "private@example.test", "private-host", "file:///"]) expect(encoded).not.toContain(secret);
		expect(result.exception?.values?.[0]?.stacktrace?.frames?.[0]).toEqual({ function: "loadProject", lineno: undefined, in_app: true });
	});

	it("does not start a client when the environment explicitly sets a blank DSN", async () => {
		vi.stubEnv("AO_SENTRY_DSN", "");
		const transport = await initMainSentry("1.0.0", "/tmp/ao-test", false);
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "error" });
		expect(sdk.init).not.toHaveBeenCalled();
		expect(sdk.captureEvent).not.toHaveBeenCalled();
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
