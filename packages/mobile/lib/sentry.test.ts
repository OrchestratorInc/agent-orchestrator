import { beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_SENTRY_DSN } from "./sentry-config";

const sdk = vi.hoisted(() => ({
	init: vi.fn(),
	setUser: vi.fn(),
	captureException: vi.fn(),
	captureMessage: vi.fn(),
	send: vi.fn(async () => ({})),
	flush: vi.fn(async () => true),
	importGate: null as Promise<void> | null,
}));

vi.mock("@sentry/react-native", async () => {
	if (sdk.importGate) await sdk.importGate;
	return {
		init: sdk.init,
		setUser: sdk.setUser,
		captureException: sdk.captureException,
		captureMessage: sdk.captureMessage,
	};
});
vi.mock("@sentry/browser", () => ({
	makeFetchTransport: () => ({ send: sdk.send, flush: sdk.flush }),
}));

beforeEach(() => {
	vi.resetModules();
	vi.clearAllMocks();
	sdk.importGate = null;
	vi.stubEnv("EXPO_PUBLIC_AO_TELEMETRY_DISABLED", undefined);
	vi.stubEnv("EXPO_PUBLIC_SENTRY_DSN", undefined);
});

describe("mobile Sentry consent and payload", () => {
	it("waits for consent and uses the existing mobile project DSN", async () => {
		const sentry = await import("./sentry");
		sentry.setMobileSentryContext({ release: "2.2.0@42", channel: "production" });
		expect(sdk.init).not.toHaveBeenCalled();
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		expect(sdk.init).toHaveBeenCalledWith(expect.objectContaining({
			dsn: DEFAULT_SENTRY_DSN,
			release: "2.2.0@42",
			environment: "stable",
			enableNative: false,
		}));
	});

	it("lets the consent-tagged global handler capture uncaught errors once", async () => {
		const sentry = await import("./sentry");
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		const options = sdk.init.mock.calls[0]?.[0];
		expect(options.integrations([
			{ name: "ReactNativeErrorHandlers" },
			{ name: "InboundFilters" },
		])).toEqual([{ name: "InboundFilters" }]);
		sentry.captureMobileException(new Error("private uncaught error"), { category: "native_crash", unhandled: true });
		const event = options.beforeSend({ event_id: "uncaught-1", tags: sdk.captureException.mock.calls[0]?.[1]?.tags });
		expect(event).toMatchObject({ event_id: "uncaught-1", message: "Mobile error" });
	});

	it("drops captures and queued sends after opt-out", async () => {
		const sentry = await import("./sentry");
		sentry.setMobileSentryContext({ release: "2.2.0@42" });
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		sentry.captureMobileException(new Error("private message"), { category: "render_crash" });
		expect(sdk.captureException).toHaveBeenCalledTimes(1);
		const options = sdk.init.mock.calls[0]?.[0];
		const transport = options.transport({});
		const safe = options.beforeSend({ type: "error", event_id: "event-1", tags: sdk.captureException.mock.calls[0]?.[1]?.tags });
		expect(safe?.event_id).toBe("event-1");
		sentry.setMobileSentryConsent(false);
		sentry.captureMobileException(new Error("private message"), { category: "render_crash" });
		expect(sdk.captureException).toHaveBeenCalledTimes(1);
		sentry.setMobileSentryConsent(true);
		await transport.send([{}, [[{ type: "event" }, { event_id: "event-1" }]]]);
		expect(sdk.send).not.toHaveBeenCalled();
		sentry.setMobileSentryConsent(false);
		expect(options.beforeSend({ message: "private message" })).toBeNull();
	});

	it("retains only the authenticated paired desktop install ID and safe frames", async () => {
		const sentry = await import("./sentry");
		sentry.setMobileSentryContext({});
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		sentry.setMobileSentryPairedInstallId("raw-session-id");
		expect(sdk.setUser).toHaveBeenLastCalledWith(null);
		const id = "ins_12345678-1234-1234-1234-123456789abc";
		sentry.setMobileSentryPairedInstallId(id);
		sentry.captureMobileException(new Error("private"), { category: "render_crash" });
		const clean = sentry.sanitizeMobileSentryEvent({
			type: undefined,
			tags: sdk.captureException.mock.calls[0]?.[1]?.tags,
			release: "private release",
			environment: "private environment",
			message: "secret project name",
			request: { url: "https://host/private" },
			extra: { token: "secret" },
			user: { id: "raw-account-id", email: "private@example.com" },
			exception: { values: [{ type: "SecretError", value: "private path", stacktrace: { frames: [
				{ function: "safeFunction", filename: "C:\\Users\\private\\repo.ts", lineno: 42 },
				{ function: "project name", abs_path: "/Users/private/repo.ts" },
			] } }] },
		});
		expect(clean).toMatchObject({ message: "Mobile error", user: { id }, exception: { values: [{
			type: "MobileError", value: "Mobile error", stacktrace: { frames: [{ function: "mobile_frame", lineno: 42 }, { function: "mobile_frame" }] },
		}] } });
		expect(JSON.stringify(clean)).not.toMatch(/private|secret|raw-account|repo/);
	});

	it("does not initialize after opt-out wins an in-flight SDK import", async () => {
		let releaseImport = () => {};
		sdk.importGate = new Promise<void>((resolve) => { releaseImport = resolve; });
		const sentry = await import("./sentry");
		sentry.setMobileSentryContext({ release: "2.2.0@42" });
		sentry.setMobileSentryConsent(true);
		const pending = sentry.initMobileSentry();
		sentry.setMobileSentryConsent(false);
		releaseImport();
		await pending;
		expect(sdk.init).not.toHaveBeenCalled();
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		expect(sdk.init).toHaveBeenCalledTimes(1);
	});

	it("honors explicit environment and DSN disable gates", async () => {
		const sentry = await import("./sentry");
		sentry.setMobileSentryContext({ release: "2.2.0@42" });
		vi.stubEnv("EXPO_PUBLIC_AO_TELEMETRY_DISABLED", "1");
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		expect(sdk.init).not.toHaveBeenCalled();
		vi.stubEnv("EXPO_PUBLIC_AO_TELEMETRY_DISABLED", undefined);
		vi.stubEnv("EXPO_PUBLIC_SENTRY_DSN", "");
		await sentry.initMobileSentry();
		expect(sdk.init).not.toHaveBeenCalled();
	});
});
