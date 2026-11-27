import { createHash } from "node:crypto";
import { AoError } from "../../shared/telemetry/ao-error";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_SENTRY_DSN } from "./sentry-config";

const digest = vi.hoisted(() => vi.fn());
vi.mock("expo-crypto", () => ({ CryptoDigestAlgorithm: { SHA256: "SHA-256" }, digestStringAsync: digest }));

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
	digest.mockReset().mockImplementation(async (_algorithm, value: string) => createHash("sha256").update(value).digest("hex"));
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
		const event = await options.beforeSend({ event_id: "uncaught-1", tags: sdk.captureException.mock.calls[0]?.[1]?.tags });
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
		const safe = await options.beforeSend({ type: "error", event_id: "event-1", tags: sdk.captureException.mock.calls[0]?.[1]?.tags });
		expect(safe?.event_id).toBe("event-1");
		sentry.setMobileSentryConsent(false);
		sentry.captureMobileException(new Error("private message"), { category: "render_crash" });
		expect(sdk.captureException).toHaveBeenCalledTimes(1);
		sentry.setMobileSentryConsent(true);
		await transport.send([{}, [[{ type: "event" }, { event_id: "event-1" }]]]);
		expect(sdk.send).not.toHaveBeenCalled();
		sentry.setMobileSentryConsent(false);
		expect(await options.beforeSend({ message: "private message" })).toBeNull();
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
		const clean = await sentry.sanitizeMobileSentryEvent({
			type: undefined,
			tags: sdk.captureException.mock.calls[0]?.[1]?.tags,
			release: "private release",
			environment: "private environment",
			message: "secret project name",
			request: { url: "https://host/private" },
			extra: { token: "secret" },
			user: { id: "raw-account-id", email: "private@example.com" },
			exception: { values: [{ type: "SecretError", value: "private ".repeat(40), stacktrace: { frames: [
				{ function: "safeFunction", filename: "C:\\Users\\private\\repo.ts", lineno: 42 },
				{ function: "project name", abs_path: "/Users/private/repo.ts" },
			] } }] },
		});
		expect(clean).toMatchObject({ message: "Mobile error", user: { id }, exception: { values: [{
			type: "Error", value: "Mobile error", stacktrace: { frames: [{ function: "mobile_frame", lineno: 42 }, { function: "mobile_frame" }] },
		}] } });
		expect(JSON.stringify(clean)).not.toMatch(/private|secret|raw-account|repo/);
	});

	it("keeps the scrubbed message and groups normalized variants", async () => {
		const sentry = await import("./sentry");
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		sentry.captureMobileException(new TypeError("Invalid array length"), { category: "render_crash" });
		const clean = (value: string) => sentry.sanitizeMobileSentryEvent({
			type: undefined,
			tags: sdk.captureException.mock.calls[0]?.[1]?.tags,
			exception: { values: [{ type: "TypeError", value }] },
		});
		const first = await clean("HTTP error 500: /etc/private http://localhost:8080/x alice@example.test");
		expect(first?.exception?.values).toMatchObject([{ type: "TypeError", value: "HTTP error <n>: <path> <url> <email> (in mobile, render_crash)" }]);
		expect((await clean("HTTP error 502: ~/private wss://host bob@example.test"))?.fingerprint).toEqual(first?.fingerprint);
		expect((await clean("Invalid array length"))?.fingerprint).not.toEqual(first?.fingerprint);
		expect((await clean("/home/alice/private"))?.message).toBe("<path>");
		const unsafe = await clean("private ".repeat(40));
		expect(unsafe?.message).toBe("Mobile error");
		expect(unsafe?.fingerprint?.slice(-2)).toEqual(["sha256", createHash("sha256").update("private ".repeat(40).trim()).digest("hex").slice(0, 10)]);
		expect(JSON.stringify(unsafe)).not.toContain("private");
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

it("groups translated mobile exceptions by the cause code", async () => {
	const sentry = await import("./sentry");
	sentry.setMobileSentryConsent(true);
	await sentry.initMobileSentry();
	const capture = async (value: string, category = "render_crash") => {
		const error = new Error(value, { cause: new AoError("RENDER_DATA_FAILED", "Data unavailable") });
		sentry.captureMobileException(error, { category });
		return sentry.sanitizeMobileSentryEvent({
			type: undefined,
			tags: sdk.captureException.mock.calls.at(-1)?.[1]?.tags,
			exception: { values: [{ type: "Error", value }] },
		});
	};
	const first = await capture("Data unavailable");
	expect(first?.message).toBe("Mobile error (RENDER_DATA_FAILED)");
	expect((await capture("Données indisponibles"))?.fingerprint).toEqual(first?.fingerprint);
	expect((await capture("Data unavailable", "native_crash"))?.fingerprint).not.toEqual(first?.fingerprint);
});

it("drops a hash in progress when consent changes", async () => {
	const sentry = await import("./sentry");
	sentry.setMobileSentryConsent(true);
	await sentry.initMobileSentry();
	sentry.captureMobileException(new Error("unsafe ".repeat(50)), { category: "render_crash" });
	let finishDigest!: (hash: string) => void;
	digest.mockReturnValueOnce(new Promise<string>((resolve) => { finishDigest = resolve; }));
	const send = sentry.sanitizeMobileSentryEvent({
		type: undefined,
		event_id: "pending-hash",
		tags: sdk.captureException.mock.calls.at(-1)?.[1]?.tags,
		exception: { values: [{ type: "Error", value: "unsafe ".repeat(50) }] },
	});
	await vi.waitFor(() => expect(digest).toHaveBeenCalledOnce());
	sentry.setMobileSentryConsent(false);
	sentry.setMobileSentryConsent(true);
	finishDigest("0123456789abc");
	expect(await send).toBeNull();
});

it.each([
	["open /Users/alice/My ConfidentialProject/offer.txt failed", "open <path> failed", /alice|ConfidentialProject|offer\.txt/],
	["request failed access_token=supersecretvalue", "request failed <credential>", /supersecretvalue/],
	["request failed refresh_token=supersecretvalue", "request failed <credential>", /supersecretvalue/],
	["request failed client_secret=supersecretvalue", "request failed <credential>", /supersecretvalue/],
	["repository acme/secret-project not found", "repository acme/secret-project not found", /secret-project/],
	["git checkout feature/acme-merger failed", "git checkout feature/acme-merger failed", /acme-merger/],
	["getaddrinfo ENOTFOUND build.acme-internal.corp", "getaddrinfo enotfound build.acme-internal.corp", /acme-internal/],
	["Session alice-laptop not found", "session alice-laptop not found", /alice-laptop/],
	["HTTP 401 Bad credentials sk-ant-abcdefghijklmnop", "http <n> bad credentials sk-ant-abcdefghijklmnop", /abcdefghijklmnop/],
] as const)("scrubs sensitive values in the final mobile event", async (value, normalized, privateText) => {
	const sentry = await import("./sentry");
	sentry.setMobileSentryConsent(true);
	await sentry.initMobileSentry();
	sentry.captureMobileException(new Error(value), { category: "render_crash" });
	const event = await sentry.sanitizeMobileSentryEvent({
		type: undefined,
		tags: sdk.captureException.mock.calls.at(-1)?.[1]?.tags,
		exception: { values: [{ type: "Error", value }] },
	});
	expect(event?.message).toBe("Mobile error");
	expect(event?.fingerprint?.at(-1)).toBe(createHash("sha256").update(normalized).digest("hex").slice(0, 10));
	expect(JSON.stringify(event)).not.toMatch(privateText);
});

it.each([
	["TypeError", undefined, "Cannot read properties of undefined (reading 'id')", "Cannot read properties of undefined (reading '?')"],
	["Error", undefined, "Cannot read properties of undefined (reading 'id')", "Mobile error"],
	["TypeError", "READ_FAILED", "Cannot read properties of undefined (reading 'id')", "Mobile error (READ_FAILED)"],
	["TypeError", undefined, "x".repeat(301), "Mobile error"],
	["TypeError", undefined, "private ".repeat(26).trim(), "Mobile error"],
	["TypeError", undefined, "Open /home/alice/config from https://host.test/path for alice@example.test 'prompt' comic-12", "Open <path> from <url> for <email> '?' <id>"],
	["Error", undefined, "Error invoking remote method 'browser:ensure': TypeError: Cannot read properties of undefined (reading 'x')", "Cannot read properties of undefined (reading '?')"],
] as const)("limits mobile message display by type, code and size", async (type, code, value, title) => {
	const sentry = await import("./sentry");
	sentry.setMobileSentryConsent(true);
	await sentry.initMobileSentry();
	sentry.captureMobileException(new Error(value), { category: "render_crash" });
	const event = await sentry.sanitizeMobileSentryEvent({
		type: undefined,
		tags: { ...sdk.captureException.mock.calls.at(-1)?.[1]?.tags, "ao.code": code },
		exception: { values: [{ type, value }] },
	});
	expect(event?.message).toBe(title);
	expect(event?.fingerprint?.at(-2)).toBe(code ? "code" : "sha256");
	expect(JSON.stringify(event)).not.toContain(value);
	if (title === "Mobile error" || code) expect(JSON.stringify(event)).not.toContain("Cannot read properties");
});

it.each([
	["SyntaxError", 'Unexpected token \'o\', "ok {"token":"abc"}" is not valid JSON', /['"]token['"]|abc/],
	["SyntaxError", 'Unexpected token \'x\', "x{"custome"... is not valid JSON', /custome/],
	["SyntaxError", "Cannot convert acme payroll to a BigInt", /acme payroll/],
	["TypeError", "Cannot add property acmePayroll, object is not extensible", /acmePayroll/],
] as const)("keeps unquoted engine input out of the mobile event", async (type, value, privateText) => {
	const sentry = await import("./sentry");
	sentry.setMobileSentryConsent(true);
	await sentry.initMobileSentry();
	sentry.captureMobileException(new Error(value), { category: "render_crash" });
	const event = await sentry.sanitizeMobileSentryEvent({
		type: undefined,
		tags: sdk.captureException.mock.calls.at(-1)?.[1]?.tags,
		exception: { values: [{ type, value }] },
	});
	expect(event?.message).toBe("Mobile error");
	expect(event?.fingerprint?.at(-2)).toBe("sha256");
	expect(JSON.stringify(event)).not.toMatch(privateText);
});

describe("mobile Sentry API context", () => {
	async function apiEvent(operation: string, setup?: (sentry: typeof import("./sentry")) => void, hostId?: string) {
		const sentry = await import("./sentry");
		setup?.(sentry);
		sentry.setMobileSentryConsent(true);
		await sentry.initMobileSentry();
		sentry.captureMobileApiError(operation, "http_5xx", 500, undefined, undefined, hostId);
		const [message, hint] = sdk.captureMessage.mock.calls.at(-1) ?? [];
		return sentry.sanitizeMobileSentryEvent({ type: undefined, message, tags: hint?.tags });
	}

	it("names and groups an API failure by its route", async () => {
		const sessions = await apiEvent("GET /api/v1/sessions");
		expect(sessions).toMatchObject({ message: "Mobile API error in GET /api/v1/sessions", tags: { "ao.operation": "GET /api/v1/sessions" } });
		const send = await apiEvent("POST /api/v1/sessions/:id/send");
		expect(send?.fingerprint).not.toEqual(sessions?.fingerprint);
	});

	// The character class alone would let a lowercase project slug through.
	it("drops an operation with a segment that is not a route word", async () => {
		const event = await apiEvent("GET /api/v1/projects/acme-secret/sessions");
		expect(event?.message).toBe("Mobile API error");
		expect(event?.tags).not.toHaveProperty("ao.operation");
		expect(JSON.stringify(event)).not.toContain("acme-secret");
	});

	it("drops an operation that is not a route name", async () => {
		const event = await apiEvent("GET /api/v1/projects/Acme Secret/sessions");
		expect(event?.message).toBe("Mobile API error");
		expect(event?.tags).not.toHaveProperty("ao.operation");
		expect(JSON.stringify(event)).not.toMatch(/Acme|Secret/);
	});

	it("tags the OTA update as dist and the version of the daemon that answered", async () => {
		const id = "0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
		const event = await apiEvent("GET /api/v1/sessions", (sentry) => {
			sentry.setMobileSentryContext({ release: "2.4.0@12", updateId: id });
			sentry.setMobileDaemonVersions({ "host-a": "1.9.0" }, true);
		}, "host-a");
		expect(event).toMatchObject({ dist: id, tags: { "ao.update_id": id, "ao.daemon_version": "1.9.0" } });
	});

	// With two desktops paired, an error names the one that served it.
	it("tags each API error with its own desktop's version", async () => {
		const versions = { "host-a": "1.9.0", "host-b": "1.7.2" };
		const fromB = await apiEvent("GET /api/v1/sessions", (sentry) => sentry.setMobileDaemonVersions(versions, true), "host-b");
		expect(fromB?.tags).toMatchObject({ "ao.daemon_version": "1.7.2" });
		const unknownHost = await apiEvent("GET /api/v1/sessions", (sentry) => sentry.setMobileDaemonVersions(versions, true));
		expect(unknownHost?.tags).not.toHaveProperty("ao.daemon_version");
	});

	it("tags an exception only when every connected desktop runs one version", async () => {
		const exceptionWith = async (versions: Record<string, string>, complete = true) => {
			const sentry = await import("./sentry");
			sentry.setMobileDaemonVersions(versions, complete);
			sentry.setMobileSentryConsent(true);
			await sentry.initMobileSentry();
			sentry.captureMobileException(new Error("boom"), { category: "render_crash" });
			return sentry.sanitizeMobileSentryEvent({ type: undefined, tags: sdk.captureException.mock.calls.at(-1)?.[1]?.tags });
		};
		expect((await exceptionWith({ a: "1.9.0", b: "1.9.0" }))?.tags).toMatchObject({ "ao.daemon_version": "1.9.0" });
		expect((await exceptionWith({ a: "1.9.0", b: "1.7.2" }))?.tags).not.toHaveProperty("ao.daemon_version");
		// B reported none (an older daemon, or its fetch failed): A's version is not B's.
		expect((await exceptionWith({ a: "1.9.0" }, false))?.tags).not.toHaveProperty("ao.daemon_version");
	});

	it("keeps a reporting desktop's version for its own API errors when another reported none", async () => {
		const event = await apiEvent("GET /api/v1/sessions", (sentry) => sentry.setMobileDaemonVersions({ "host-a": "1.9.0" }, false), "host-a");
		expect(event?.tags).toMatchObject({ "ao.daemon_version": "1.9.0" });
	});

	it("forgets the daemon versions when no desktop is connected", async () => {
		const event = await apiEvent("GET /api/v1/sessions", (sentry) => {
			sentry.setMobileDaemonVersions({ "host-a": "1.9.0" }, true);
			sentry.setMobileDaemonVersions({}, false);
		}, "host-a");
		expect(event?.tags).not.toHaveProperty("ao.daemon_version");
	});
});
