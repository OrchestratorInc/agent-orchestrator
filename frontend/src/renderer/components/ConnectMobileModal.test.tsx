import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { TooltipProvider } from "./ui/tooltip";
import { apiClient } from "../lib/api-client";

// vi.mock is hoisted above module-level consts, so the shared double has to be
// created inside vi.hoisted to exist by the time the factory runs.
const { mobileStatus } = vi.hoisted(() => ({
	mobileStatus: {
		enabled: true,
		noPublicTunnel: false,
		host: "192.168.1.42",
		tailscaleHost: "100.72.46.7",
		port: 3011,
		password: "fake-password-for-testing",
		// The daemon always advertises its endpoints now, and a v2 code is built
		// from that list — there is no v1 form left to fall back to when it is
		// missing, so a fixture without it is not a realistic daemon.
		hostId: "h_fixture",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
		tunnel: undefined as
			| undefined
			| {
					supported: boolean;
					running: boolean;
					ready: boolean;
					hostname: string;
					location: string;
					lastError: string;
			  },
		warning: "",
		securePairing: {
			enabled: false,
			available: false,
			active: false,
			host: "",
			port: 0,
			reason: "",
		},
		keepAwake: undefined as
			| undefined
			| { supported: boolean; enabled: boolean; active: boolean; hasBattery: boolean; lidClosed?: boolean; lidSupported?: boolean; lidActive?: boolean; lidReason?: string },
	},
}));

// This suite exercises the mobile UI; shared desktop/mobile ownership is
// covered with the real useAwake hook in AwakeControl.test.tsx.
vi.mock("../hooks/useAwake", () => ({
	useAwake: () => ({ state: { sessions: [], rules: [], capabilities: [{ id: "idle", available: true }] }, actAsync: vi.fn().mockResolvedValue({ active: true }), busy: false }),
	awakeCapability: () => ({ available: true }),
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: async (path: string) =>
			path === "/api/v1/mobile/devices"
				? { data: { devices: [] }, error: undefined }
				: { data: mobileStatus, error: undefined },
		POST: vi.fn(async () => ({ data: structuredClone(mobileStatus), error: undefined })),
	},
	apiErrorMessage: () => "failed",
}));

import {
	ConnectMobileContent,
	mobileStatusQueryKey,
	mobileStatusRefetchInterval,
	pairingPayload,
	localOnlyReason,
	qrIsReady,
	qrValueFor,
} from "./settings/ConnectMobileContent";

function renderMobileSettings(
	client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ConnectMobileContent active />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

// Read the payload off the wrapper used by the shared styled QR component.
function qrPayload(): string | null {
	const qr = document.querySelector("[data-qr-value]");
	return qr?.getAttribute("data-qr-value") ?? null;
}

// Mirrors what the phone does with a scanned code.
function decodeQr(value: string): Record<string, unknown> {
	const code = value.slice(value.indexOf("#") + 1);
	const b64 = code.replace(/-/g, "+").replace(/_/g, "/");
	return JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4)));
}

async function selectConnectionMethod(mode: "LAN" | "Tailscale") {
	await userEvent.click(await screen.findByRole("button", { name: "Connection method" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: mode }));
}

beforeEach(() => {
	// Keep awake's last-known state is remembered across mounts; start clean.
	window.localStorage.clear();
	mobileStatus.enabled = true;
	mobileStatus.password = "fake-password-for-testing";
	mobileStatus.noPublicTunnel = false;
	mobileStatus.host = "192.168.1.42";
	mobileStatus.hostId = "h_fixture";
	mobileStatus.tunnel = undefined;
	mobileStatus.endpoints = [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }];
	mobileStatus.tailscaleHost = "100.72.46.7";
	mobileStatus.warning = "";
	mobileStatus.securePairing = {
		enabled: false,
		available: false,
		active: false,
		host: "",
		port: 0,
		reason: "",
	};
	mobileStatus.keepAwake = undefined;
	vi.mocked(apiClient.POST).mockClear();
});

test("QR payload carries host, port, and password for one-scan connect", () => {
	const s = pairingPayload("192.168.1.42", 3011, "fake-password-for-testing");
	expect(JSON.parse(s)).toEqual({ v: 1, host: "192.168.1.42", port: 3011, password: "fake-password-for-testing" });
});

test("encodes the LAN address by default", async () => {
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());
	const offer = decodeQr(qrPayload()!);
	expect(offer.endpoints).toContainEqual(
		expect.objectContaining({ kind: "lan", host: "192.168.1.42" }),
	);
	expect(offer.token).toBe("fake-password-for-testing");
	expect(offer.hostId).toBe("h_fixture");
	expect(screen.getByTestId("mobile-pairing-address")).toHaveTextContent("192.168.1.42:3011");
});

test("uses advertised LAN addresses instead of the legacy host and port", async () => {
	mobileStatus.endpoints = [{ kind: "lan", host: "192.168.1.43", port: 4011, secure: false }];
	renderMobileSettings();

	await waitFor(() => expect(qrPayload()).not.toBeNull());
	expect(decodeQr(qrPayload()!).endpoints).toEqual(mobileStatus.endpoints);
	expect(screen.getByTestId("mobile-pairing-address")).toHaveTextContent("192.168.1.43:4011");
});

test("shows the tunnel-only QR without a LAN address and preserves pairing credentials", async () => {
	mobileStatus.host = "";
	mobileStatus.tailscaleHost = "";
	mobileStatus.endpoints = [{ kind: "tunnel", host: "host.example.net", port: 443, secure: true }];
	mobileStatus.tunnel = {
		supported: true, running: true, ready: true, hostname: "host.example.net", location: "", lastError: "",
	};
	vi.mocked(apiClient.POST).mockClear();
	renderMobileSettings();

	await waitFor(() => expect(qrPayload()).not.toBeNull());
	expect(decodeQr(qrPayload()!)).toEqual({
		v: 2,
		hostId: "h_fixture",
		name: "host.example.net",
		platform: "",
		endpoints: mobileStatus.endpoints,
		token: "fake-password-for-testing",
	});
	expect(await screen.findByTestId("mobile-pairing-address")).toHaveTextContent("host.example.net:443");
	expect(screen.queryByText(/No network address found/i)).not.toBeInTheDocument();
	expect(apiClient.POST).not.toHaveBeenCalled();
});

test("waits for a tunnel-only endpoint instead of reporting a missing LAN address", async () => {
	mobileStatus.host = "";
	mobileStatus.tailscaleHost = "";
	mobileStatus.endpoints = [];
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	expect(await screen.findByTestId("mobile-pairing-preparing")).toBeVisible();
	expect(qrPayload()).toBeNull();
	expect(screen.queryByText(/No network address found/i)).not.toBeInTheDocument();
	expect(screen.queryByTestId("mobile-pairing-address")).not.toBeInTheDocument();
});

test("withholds the QR when tunnel startup fails without advertising an endpoint", async () => {
	mobileStatus.host = "";
	mobileStatus.tailscaleHost = "";
	mobileStatus.endpoints = [];
	mobileStatus.tunnel = {
		supported: true, running: false, ready: false, hostname: "", location: "", lastError: "connector exited",
	};
	renderMobileSettings();

	expect(await screen.findByText(/No network address found/i)).toBeVisible();
	expect(qrPayload()).toBeNull();
	expect(screen.queryByTestId("mobile-pairing-preparing")).not.toBeInTheDocument();
	expect(screen.queryByTestId("mobile-pairing-address")).not.toBeInTheDocument();
});

test("vertically aligns the regenerate control with the password row", async () => {
	renderMobileSettings();

	// The password value and its icon actions share one centred flex row.
	const regenerate = await screen.findByRole("button", { name: "Regenerate password" });
	const row = regenerate.closest("dd");
	expect(row).toHaveClass("flex", "items-center");
	expect(row).toHaveTextContent("fake-password-for-testing");
});

// The blurred placeholder stays behind the spinner, so the code that arrives
// resolves in place; what matters is that nothing scannable shows yet.
test("shows a quiet preparing state while startup is incomplete", async () => {
	mobileStatus.endpoints = [];
	mobileStatus.tunnel = {
		supported: true,
		running: false,
		ready: false,
		hostname: "",
		location: "",
		lastError: "",
	};

	renderMobileSettings();

	expect(await screen.findByTestId("mobile-pairing-preparing")).toBeInTheDocument();
	expect(screen.queryByText(/Preparing remote access/i)).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Generate" })).not.toBeInTheDocument();
	expect(qrPayload()).toBeNull();
});

test("can turn off the generated mobile connection", async () => {
	renderMobileSettings();
	const button = await screen.findByRole("button", { name: "Turn off mobile connection" });

	await userEvent.click(button);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/disable");
});

test.each([
	{ outcome: "success", serverEnabled: false, failed: false },
	{ outcome: "error after shutdown", serverEnabled: false, failed: true },
	{ outcome: "error while still running", serverEnabled: true, failed: true },
])("refreshes mobile status after disable $outcome", async ({ serverEnabled, failed }) => {
	let statusReads = 0;
	const get = vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
		if (path === "/api/v1/mobile/devices") {
			return { data: { devices: [] }, error: undefined };
		}
		statusReads++;
		// Each response is a snapshot. Updating the fake server must not mutate
		// the cached response and conceal a missing status refetch.
		return { data: { ...mobileStatus }, error: undefined };
	});
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		mobileStatus.enabled = serverEnabled;
		return failed
			? { data: undefined, error: { message: "shutdown failed" } }
			: { data: structuredClone(mobileStatus), error: undefined };
	});

	try {
		renderMobileSettings();
		await userEvent.click(await screen.findByRole("button", { name: "Turn off mobile connection" }));
		await waitFor(() => expect(statusReads).toBeGreaterThan(1));
		await waitFor(() => {
			const disableButton = screen.queryByRole("button", { name: "Turn off mobile connection" });
			if (serverEnabled) {
				expect(disableButton).toBeVisible();
				expect(qrPayload()).not.toBeNull();
			} else {
				expect(disableButton).not.toBeInTheDocument();
				expect(screen.getByRole("button", { name: "Generate" })).toBeVisible();
			}
		});
		if (failed) expect(screen.getByText("failed")).toBeVisible();
	} finally {
		get.mockRestore();
	}
});

// SKIPPED: drives the connection picker, which is commented out in
// ConnectMobileContent. Un-skip with it — the behaviour is unchanged, only
// the way in is gone. TLS coverage does NOT live here any more: it keys off
// the tailnet address, so those tests run without the picker.
test.skip("keeps the connection-method dropdown at its fixed width", async () => {
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	const control = screen.getByRole("button", { name: "Connection method" });
	expect(control).toHaveClass("w-44", "justify-between");

	await userEvent.click(control);
	expect(screen.getByRole("menu")).toHaveClass("!w-44", "!min-w-0");
});

// A code without a tunnel says so itself (localOnlyReason) and the
// no-connector case is stated by remoteAccessUnavailable, so the generic
// intro must not tell everyone to join the same Wi-Fi.
test("does not tell LAN users to join the same Wi-Fi", async () => {
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.queryByText(/same Wi-Fi/i)).not.toBeInTheDocument();
	expect(screen.getByText("Scan with the Orchestrator mobile app")).toBeInTheDocument();
});

// Both stores are listed in step one now. The platform dropdown that used to
// gate them is gone, so neither link may be behind an interaction.
test("offers both store links without a platform choice", async () => {
	renderMobileSettings();

	expect(await screen.findByRole("button", { name: "Open Agent Orchestrator on the App Store" })).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Open Agent Orchestrator on Google Play" })).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Get the app" })).not.toBeInTheDocument();
});

// The store links used to be muted text links, easy to read past. They are a
// filled segmented control now, next to "Get the app".
test("shows the store links as one segmented control beside Get the app", async () => {
	renderMobileSettings();

	const ios = await screen.findByRole("button", { name: "Open Agent Orchestrator on the App Store" });
	const android = screen.getByRole("button", { name: "Open Agent Orchestrator on Google Play" });
	expect(ios.parentElement).toBe(android.parentElement);
	expect(ios.parentElement).toHaveClass("bg-(--color-bg-settings-input)");
	expect(ios.parentElement?.parentElement).toHaveTextContent("Get the app");
});

test("shows a square Google Play QR tooltip for Android", async () => {
	renderMobileSettings();
	await userEvent.hover(await screen.findByRole("button", { name: "Open Agent Orchestrator on Google Play" }));

	expect(await screen.findByTestId("android-play-qr")).toHaveClass("p-2");
});

test("shows a QR-only App Store tooltip", async () => {
	renderMobileSettings();
	await userEvent.hover(await screen.findByRole("button", { name: "Open Agent Orchestrator on the App Store" }));

	const tooltip = await screen.findByTestId("ios-store-qr");
	expect(tooltip).not.toHaveTextContent("App Store");
	expect(tooltip.querySelector("canvas")).toBeInTheDocument();
});

// v1 encoded one chosen address, so switching mode had to re-encode the code.
// A v2 code carries every endpoint the daemon advertises and the phone races
// them, so the mode selector now only changes which address is *displayed* —
// the code itself is the same either way. Re-encoding per mode would defeat the
// race by handing the phone a single path again.
// SKIPPED: drives the connection picker, which is commented out in
// ConnectMobileContent. Un-skip with it — the behaviour is unchanged, only
// the way in is gone. TLS coverage does NOT live here any more: it keys off
// the tailnet address, so those tests run without the picker.
test.skip("keeps one code across modes, carrying every advertised endpoint", async () => {
	mobileStatus.endpoints = [
		{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
		{ kind: "tailscale", host: "100.72.46.7", port: 3011, secure: false },
	];
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());
	const before = qrPayload()!;

	await selectConnectionMethod("Tailscale");
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(decodeQr(qrPayload()!).endpoints).toEqual([
		{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
		{ kind: "tailscale", host: "100.72.46.7", port: 3011, secure: false },
	]);
	expect(decodeQr(before).endpoints).toEqual(decodeQr(qrPayload()!).endpoints);
});

// SKIPPED: drives the connection picker, which is commented out in
// ConnectMobileContent. Un-skip with it — the behaviour is unchanged, only
// the way in is gone. TLS coverage does NOT live here any more: it keys off
// the tailnet address, so those tests run without the picker.
test.skip("shows a hint instead of a QR when Tailscale is not running", async () => {
	mobileStatus.tailscaleHost = "";
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	await selectConnectionMethod("Tailscale");

	await waitFor(() => expect(screen.getByText(/Tailscale isn't running/i)).toBeInTheDocument());
	expect(qrPayload()).toBeNull();
});

test("shows a hint instead of an unscannable QR when no endpoint is available", async () => {
	mobileStatus.host = "";
	mobileStatus.tailscaleHost = "";
	mobileStatus.endpoints = [];
	mobileStatus.tunnel = {
		supported: false, running: false, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();
	await waitFor(() => expect(screen.getByText(/No network address found/i)).toBeInTheDocument());
	expect(qrPayload()).toBeNull();
	expect(screen.queryByTestId("mobile-pairing-preparing")).not.toBeInTheDocument();
});

test.each([
	{ name: "empty", endpoints: [] },
	{ name: "null", endpoints: null },
])("shows the network hint for LAN-only mode with $name endpoints and an installed connector", async ({ endpoints }) => {
	mobileStatus.noPublicTunnel = true;
	mobileStatus.host = "";
	mobileStatus.tailscaleHost = "";
	Reflect.set(mobileStatus, "endpoints", endpoints);
	mobileStatus.tunnel = {
		supported: true, running: false, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	expect(await screen.findByText(/No network address found/i)).toBeVisible();
	expect(screen.queryByTestId("mobile-pairing-preparing")).not.toBeInTheDocument();
	expect(screen.queryByTestId("mobile-pairing-address")).not.toBeInTheDocument();
	expect(qrPayload()).toBeNull();
});

// SKIPPED: drives the connection picker, which is commented out in
// ConnectMobileContent. Un-skip with it — the behaviour is unchanged, only
// the way in is gone. TLS coverage does NOT live here any more: it keys off
// the tailnet address, so those tests run without the picker.
test.skip("the address line follows the selected mode", async () => {
	renderMobileSettings();
	const address = await screen.findByTestId("mobile-pairing-address");
	expect(within(address).getByText("192.168.1.42:3011")).toBeInTheDocument();

	await selectConnectionMethod("Tailscale");
	await waitFor(() => expect(within(address).getByText("100.72.46.7:3011")).toBeInTheDocument());
});

test("omits the secure key entirely for plaintext pairing", () => {
	expect(JSON.parse(pairingPayload("192.168.1.42", 3011, "pw"))).toEqual({
		v: 1, host: "192.168.1.42", port: 3011, password: "pw",
	});
});

test("encodes secure:true when secure pairing is active", () => {
	expect(JSON.parse(pairingPayload("host.tail1.ts.net", 443, "pw", true))).toEqual({
		v: 1, host: "host.tail1.ts.net", port: 443, password: "pw", secure: true,
	});
});

// KNOWN GAP, recorded rather than asserted away: a v2 code is built purely from
// the daemon's endpoint list, and Endpoints() only ever marks the tunnel
// secure — tailscale entries are the plain tailnet IP. So the MagicDNS host
// secure pairing sets up is not carried in the code at all.
//
// This predates the v1 removal; it was hidden because the old fixture
// advertised no endpoints, which forced the v1 branch that did encode it. The
// tunnel's publicly trusted certificate is what satisfies iOS ATS now, so this
// may be intentional obsolescence — but the setting is still offered, and a
// user who enables it gets a code that does not use it.
// SKIPPED: drives the connection picker, which is commented out in
// ConnectMobileContent. Un-skip with it — the behaviour is unchanged, only
// the way in is gone. TLS coverage does NOT live here any more: it keys off
// the tailnet address, so those tests run without the picker.
test.skip("does not carry the secure-pairing MagicDNS host in the code", async () => {
	mobileStatus.securePairing = {
		enabled: true, available: true, active: true,
		host: "prasads-macbook-pro.tail057d04.ts.net", port: 443, reason: "",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());
	await selectConnectionMethod("Tailscale");

	await waitFor(() => expect(qrPayload()).not.toBeNull());
	const hosts = (decodeQr(qrPayload()!).endpoints as { host: string }[]).map((e) => e.host);
	expect(hosts).not.toContain("prasads-macbook-pro.tail057d04.ts.net");
});

// SKIPPED: drives the connection picker, which is commented out in
// ConnectMobileContent. Un-skip with it — the behaviour is unchanged, only
// the way in is gone. TLS coverage does NOT live here any more: it keys off
// the tailnet address, so those tests run without the picker.
test.skip("shows setup steps and no QR when certs are not enabled", async () => {
	mobileStatus.securePairing = {
		enabled: true, available: false, active: false,
		host: "h.tail1.ts.net", port: 0, reason: "no_certs",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());
	await selectConnectionMethod("Tailscale");

	await waitFor(() => expect(screen.getByText(/HTTPS certificates/i)).toBeInTheDocument());
	expect(qrPayload()).toBeNull();
});

// A failing secure-pairing POST must surface an error rather than silently
// snapping the switch back on the next status refetch with no explanation.
test("surfaces an error when enabling secure pairing fails", async () => {
	mobileStatus.securePairing = {
		enabled: false, available: true, active: false,
		host: "", port: 0, reason: "",
	};
	const { apiClient } = await import("../lib/api-client");
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => ({
		data: undefined,
		error: { message: "secure pairing failed" },
	}));
	renderMobileSettings();

	await waitFor(() => expect(screen.getByText("failed")).toBeInTheDocument());
});

// TLS is no longer a switch: iOS refuses cleartext to a 100.x address, so a
// Tailscale pairing with it off works on Android and fails on iPhone with
// nothing to explain why. Selecting Tailscale turns it on.
test("turns secure pairing on wherever a tailnet address exists", async () => {
	mobileStatus.securePairing = {
		enabled: false, available: true, active: false,
		host: "", port: 0, reason: "",
	};
	const { apiClient } = await import("../lib/api-client");
	renderMobileSettings();

	await waitFor(() =>
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/secure-pairing", { body: { enabled: true } }),
	);
	expect(screen.queryByRole("switch", { name: "Secure pairing (TLS)" })).not.toBeInTheDocument();
	// Nothing to report while it works: the panel guarantees TLS, so only a
	// failure earns space.
	expect(screen.queryByTestId("secure-pairing-reason")).not.toBeInTheDocument();
});

// A tailnet with no certificates rejects every attempt. Retrying on each status
// poll would hammer the daemon; the reason text is what tells the user.
test("does not retry enabling secure pairing when it is unavailable", async () => {
	mobileStatus.securePairing = {
		enabled: false, available: false, active: false,
		host: "", port: 0, reason: "no_certs",
	};
	const { apiClient } = await import("../lib/api-client");
	// POST is a suite-wide mock; a previous test's secure-pairing call would
	// otherwise satisfy the negative assertion below.
	vi.mocked(apiClient.POST).mockClear();
	renderMobileSettings();

	await waitFor(() => expect(screen.getByTestId("secure-pairing-reason")).toBeInTheDocument());
	expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/mobile/secure-pairing", { body: { enabled: true } });
});


// The QR value is the wire contract with the phone.
test("emits a v2 deep link carrying every endpoint once the daemon advertises them", () => {
	const value = qrValueFor({
		hostId: "h_b3e07f31",
		host: "192.168.1.42",
		platform: "darwin",
		password: "pw",
		endpoints: [
			{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
			{ kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true },
		],
	});

	expect(value.startsWith("aomobile://pair#")).toBe(true);
	const code = value.slice(value.indexOf("#") + 1);
	const b64 = code.replace(/-/g, "+").replace(/_/g, "/");
	const decoded = JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4)));
	expect(decoded.v).toBe(2);
	expect(decoded.hostId).toBe("h_b3e07f31");
	expect(decoded.endpoints).toHaveLength(2);
});

// The token must never sit in the part of a URL that reaches a server.
test("keeps the connection token out of everything before the fragment", () => {
	const value = qrValueFor({
		hostId: "h_x", host: "192.168.1.42", platform: "darwin", password: "super-secret",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});

	expect(value.split("#")[0]).not.toContain("super-secret");
});

// v1 emission was removed alongside the phone's v1 support: those codes predate
// the identity probe the race uses, so a daemon old enough to need one could
// never complete a race. An empty endpoint list is the "preparing" state, not a
// reason to emit a code the phone will refuse — see qrIsReady.
test("emits only v2, never a raw JSON v1 payload", () => {
	const value = qrValueFor({
		hostId: "h_x", host: "192.168.1.42", platform: "darwin", password: "pw",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});

	expect(value.startsWith("aomobile://pair#")).toBe(true);
	// v1 travelled as bare JSON; a v2 link must never parse as one.
	expect(() => JSON.parse(value)).toThrow();
	expect(decodeQr(value).v).toBe(2);
});

// Local pairing should not wait on optional remote access. The status query
// keeps polling while the tunnel starts, so the rendered offer gains the
// remote endpoint when it becomes available without blocking the LAN code.
test("shows the LAN QR while remote access is still starting", () => {
	expect(
		qrIsReady({
			enabled: true,
			endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
			tunnel: { running: true, ready: false, hostname: "", location: "", lastError: "" },
		}),
	).toBe(true);
});

test("shows the QR once the tunnel is advertisable", () => {
	expect(
		qrIsReady({
			enabled: true,
			endpoints: [
				{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
				{ kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true },
			],
			tunnel: { running: true, ready: true, hostname: "abc.trycloudflare.com", location: "", lastError: "" },
		}),
	).toBe(true);
});

// Remote access unavailable entirely (no cloudflared) must not block pairing —
// LAN-only is a legitimate setup and waiting forever would be worse.
test("shows the QR when there is no tunnel to wait for", () => {
	expect(
		qrIsReady({
			enabled: true,
			endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
			tunnel: { supported: false, running: false, ready: false, hostname: "", location: "", lastError: "" },
		}),
	).toBe(true);
});

test("shows the LAN QR before a supported tunnel starts", () => {
	expect(
		qrIsReady({
			enabled: true,
			endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
			tunnel: { supported: true, running: false, ready: false, hostname: "", location: "", lastError: "" },
		}),
	).toBe(true);
});

// The QR shows before the tunnel is up, so a code scanned or photographed in
// that window carries no tunnel and fails away from home. Seen in the field: a
// photographed code held only the LAN and Tailscale addresses, and the phone
// reported "same Wi-Fi?" from mobile data with nothing on the desktop to warn.
// The code refreshes itself once the tunnel is up; until then the note under
// it says remote access is still being enabled.
test("says remote access is enabling while the tunnel starts", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.getByTestId("mobile-remote-access-starting")).toHaveTextContent("Enabling remote access");
	expect(screen.getByTestId("mobile-remote-access-starting")).toHaveTextContent("Until then, works on this network only");
	expect(screen.queryByTestId("mobile-remote-access-on")).not.toBeInTheDocument();
	expect(screen.queryByTestId("mobile-local-only")).not.toBeInTheDocument();
});

// Tailscale reaches this machine from another network once both devices are
// on the tailnet, so a code carrying one must not be called "this network only".
test("keeps Tailscale as the remote path while the public tunnel is missing", async () => {
	mobileStatus.endpoints = [
		{ kind: "lan", host: "192.168.88.7", port: 3011, secure: false },
		{ kind: "tailscale", host: "100.72.46.7", port: 3011, secure: false },
	];
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	const note = screen.getByTestId("mobile-remote-access-starting");
	expect(note).toHaveTextContent("Enabling remote access");
	expect(note).toHaveTextContent(/only over Tailscale/);
	expect(note).not.toHaveTextContent(/this network only/i);
});

// The note under the code comes and goes with the tunnel, but the code itself
// must stay the same element: a remount would drop PairingQr's crossfade and
// pop the next code in (or the last one out) with no transition.
test("keeps the same code element as the remote access note changes", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	renderMobileSettings(client);
	await waitFor(() => expect(qrPayload()).not.toBeNull());
	expect(screen.getByTestId("mobile-remote-access-starting")).toBeInTheDocument();
	const square = screen.getByTestId("mobile-pairing-panel").firstElementChild;
	const canvas = document.querySelector("[data-qr-value]");

	// starting -> failed: the note goes, the code is unchanged.
	act(() => {
		client.setQueryData(mobileStatusQueryKey, {
			...structuredClone(mobileStatus),
			tunnel: { supported: true, running: false, ready: false, hostname: "", location: "", lastError: "connector exited" },
		});
	});
	await waitFor(() => expect(screen.queryByTestId("mobile-remote-access-starting")).not.toBeInTheDocument());
	expect(screen.getByTestId("mobile-local-only")).toBeInTheDocument();
	expect(screen.getByTestId("mobile-pairing-panel").firstElementChild).toBe(square);
	expect(document.querySelector("[data-qr-value]")).toBe(canvas);

	// -> on: the code gains the tunnel and crossfades inside the same square.
	act(() => {
		client.setQueryData(mobileStatusQueryKey, {
			...structuredClone(mobileStatus),
			endpoints: [
				{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
				{ kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true },
			],
			tunnel: { supported: true, running: true, ready: true, hostname: "abc.trycloudflare.com", location: "", lastError: "" },
		});
	});
	await waitFor(() => expect(screen.getByTestId("mobile-remote-access-on")).toBeInTheDocument());
	expect(screen.getByTestId("mobile-pairing-panel").firstElementChild).toBe(square);
});

test("keeps Tailscale as the remote path when remote access failed", async () => {
	mobileStatus.endpoints = [{ kind: "tailscale", host: "100.72.46.7", port: 3011, secure: false }];
	mobileStatus.tunnel = {
		supported: true, running: false, ready: false, hostname: "", location: "", lastError: "connector exited",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.getByTestId("mobile-local-only")).toHaveTextContent(/No public tunnel — remote access couldn't start.*only over Tailscale/);
});

test("drops the label once the code carries the tunnel", async () => {
	mobileStatus.endpoints = [
		{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
		{ kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true },
	];
	mobileStatus.tunnel = {
		supported: true, running: true, ready: true, hostname: "abc.trycloudflare.com", location: "", lastError: "",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.queryByTestId("mobile-local-only")).not.toBeInTheDocument();
	expect(screen.getByTestId("mobile-remote-access-on")).toHaveTextContent("Remote access on");
});

test("does not claim remote access for a LAN-only code", async () => {
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.queryByTestId("mobile-remote-access-on")).not.toBeInTheDocument();
});

test("says a LAN-only code stays local when remote access failed", async () => {
	mobileStatus.tunnel = {
		supported: true, running: false, ready: false, hostname: "", location: "", lastError: "connector exited",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.getByTestId("mobile-local-only")).toHaveTextContent(/remote access couldn't start/i);
});

test("reports why a code has no public tunnel", () => {
	const lan = [{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false }];
	const starting = { supported: true, running: true, ready: false, lastError: "" };
	expect(localOnlyReason({ enabled: true, endpoints: lan, tunnel: starting })).toBe("starting");
	expect(localOnlyReason({ enabled: true, endpoints: lan, tunnel: { ...starting, running: false, lastError: "boom" } })).toBe("failed");
	// Covered by remoteAccessUnavailable, or the user's own choice.
	expect(localOnlyReason({ enabled: true, endpoints: lan, tunnel: { ...starting, supported: false, running: false } })).toBeNull();
	expect(localOnlyReason({ enabled: true, noPublicTunnel: true, endpoints: lan, tunnel: starting })).toBeNull();
	// Nothing on screen to judge.
	expect(localOnlyReason({ enabled: true, endpoints: [], tunnel: starting })).toBeNull();
	expect(localOnlyReason({ enabled: false, endpoints: lan, tunnel: starting })).toBeNull();
});

test("holds the QR back when nothing is reachable yet", () => {
	expect(qrIsReady({ enabled: true, endpoints: [], tunnel: undefined })).toBe(false);
});

// A v2 code is built from the endpoint list and there is no v1 form left to
// fall back to, so an absent list is as unready as an empty one: the daemon has
// not yet said where it can be reached. Emitting a code here would produce one
// the phone refuses.
test("withholds the QR from a daemon that reports no endpoints", () => {
	expect(qrIsReady({ enabled: true, endpoints: undefined, tunnel: undefined })).toBe(false);
	expect(qrIsReady({ enabled: true, endpoints: [], tunnel: undefined })).toBe(false);
});

// The QR gate introduced a state the renderer has to wait out. The status query
// is fetched once on open and refetched only after a mutation, so without
// polling the modal sits on "Preparing remote access…" forever even though the
// daemon went advertisable seconds later.
test("polls while the connector is starting", () => {
	expect(
		mobileStatusRefetchInterval({
			enabled: true,
			endpoints: [],
			tunnel: { supported: true, running: false, ready: false },
		}),
	).toBeGreaterThan(0);
	expect(
		mobileStatusRefetchInterval({
			enabled: true,
			endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
			tunnel: { supported: true, running: true, ready: false },
		}),
	).toBeGreaterThan(0);
});

// Polling only while there is something to wait for: once the tunnel is up
// there is no transient state left, and the modal should not keep hitting the
// daemon for the rest of the session.
test("stops polling once the tunnel is advertisable", () => {
	expect(
		mobileStatusRefetchInterval({
			enabled: true,
			endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
			tunnel: { supported: true, running: true, ready: true },
		}),
	).toBe(false);
});

test("does not poll when there is no tunnel to wait for", () => {
	const endpoints = [{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false }];
	expect(
		mobileStatusRefetchInterval({
			enabled: true,
			endpoints,
			tunnel: { supported: false, running: false, ready: false },
		}),
	).toBe(false);
	expect(mobileStatusRefetchInterval({ enabled: true, endpoints, tunnel: undefined })).toBe(false);
	expect(mobileStatusRefetchInterval(undefined)).toBe(false);
});

test("polls LAN-only mode for network recovery but not for a disabled public tunnel", () => {
	const status = {
		enabled: true,
		noPublicTunnel: true,
		tunnel: { supported: true, running: false, ready: false, lastError: "" },
	};
	expect(mobileStatusRefetchInterval({ ...status, endpoints: [] })).toBeGreaterThan(0);
	expect(mobileStatusRefetchInterval({
		...status,
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	})).toBe(false);
});

// Remote access is optional: nothing installs cloudflared, so on a machine
// without it there is no connector at all. A zero tunnel status made that
// indistinguishable from "not started yet", so the QR looked entirely normal
// and the user discovered the gap only by being away from home.
test("says so when this machine cannot be reached from elsewhere", async () => {
	mobileStatus.tunnel = {
		supported: false, running: false, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	expect(await screen.findByTestId("mobile-remote-unavailable")).toHaveTextContent(
		/only|cloudflared/i,
	);
});

// A machine that has a connector must not carry the caveat, whether or not the
// tunnel happens to be up yet.
test("does not claim unavailability while the connector is merely starting", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	await waitFor(() => expect(screen.queryByTestId("mobile-remote-unavailable")).toBeNull());
});

// The install offer belongs with the caveat: the user learns that remote access
// is unavailable and can act on it in the same place, rather than being told to
// go and find a terminal.
test("offers to install the connector when it is missing", async () => {
	mobileStatus.tunnel = {
		supported: false, running: false, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	expect(await screen.findByTestId("mobile-install-cloudflared")).toBeInTheDocument();
});

test("does not offer an install when a connector already exists", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: true, hostname: "x.trycloudflare.com", location: "", lastError: "",
	};
	renderMobileSettings();

	await waitFor(() => expect(screen.queryByTestId("mobile-install-cloudflared")).toBeNull());
});

// macOS only: the daemon reports the option unsupported elsewhere, and an older
// daemon omits the block entirely. Neither should show a switch that cannot work.
test.each([
	{ name: "absent", keepAwake: undefined },
	{ name: "unsupported", keepAwake: { supported: false, enabled: false, active: false, hasBattery: false } },
])("hides the keep-awake option when it is $name", async ({ keepAwake }) => {
	mobileStatus.keepAwake = keepAwake;
	renderMobileSettings();
	await screen.findByRole("button", { name: "Turn off mobile connection" });
	expect(screen.queryByTestId("mobile-keep-awake")).not.toBeInTheDocument();
});

test("turns the keep-awake option on", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: false };
	renderMobileSettings();

	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	expect(screen.queryByTestId("mobile-keep-awake-laptop")).not.toBeInTheDocument();

	// The daemon persists the choice, so the refetch after the POST reads it back.
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: false };
		return { data: structuredClone(mobileStatus), error: undefined } as never;
	});
	await userEvent.click(toggle);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: false } });
	// Flipped immediately and never falls back to off while the refetch lands.
	expect(toggle).toBeChecked();
	await waitFor(() => expect(toggle).toBeChecked());
});

// Closing a MacBook's lid sleeps it regardless, so laptops get told up front.
test("warns laptops that closing the lid still sleeps them", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true };
	renderMobileSettings();

	expect(await screen.findByRole("switch", { name: "Keep this Mac awake" })).toBeChecked();
	expect(screen.getByTestId("mobile-keep-awake-laptop")).toBeInTheDocument();
});

test("turning on closed-lid mode also enables idle wake in one request", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: true, lidSupported: true, lidClosed: false };
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		Object.assign(mobileStatus.keepAwake!, { enabled: true, lidClosed: true });
		return { data: structuredClone(mobileStatus), error: undefined };
	});
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep awake with lid closed" });
	expect(toggle).not.toBeChecked();
	await userEvent.click(toggle);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: true } });
	await waitFor(() => expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toBeChecked());
	expect(toggle).toBeChecked();
});

test("shows a latched low-battery stop without claiming native activation", async () => {
	mobileStatus.keepAwake = {
		supported: true,
		enabled: true,
		active: true,
		hasBattery: true,
		lidSupported: true,
		lidClosed: true,
		lidActive: false,
		lidReason: "low_battery",
	};
	renderMobileSettings();
	expect(await screen.findByRole("switch", { name: "Keep awake with lid closed" })).toBeChecked();
	expect(screen.getByText(/Stopped below 10% battery/)).toBeInTheDocument();
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
});

test("keeps lid controls hidden for older daemons", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true };
	renderMobileSettings();
	await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(screen.queryByRole("switch", { name: "Keep awake with lid closed" })).not.toBeInTheDocument();
});

test.each([false, true])("shows helper upgrade recovery with lid preference %s", async (lidClosed) => {
	mobileStatus.keepAwake = {
		supported: true,
		enabled: true,
		active: true,
		hasBattery: true,
		lidSupported: true,
		lidClosed,
		lidActive: false,
		lidReason: "helper_upgrade_required",
	};
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep awake with lid closed" });
	expect(toggle).toHaveAttribute("aria-checked", String(lidClosed));
	expect(screen.getByText(/launchctl bootout gui\/\$\(id -u\)\/com\.aoagents\.mobile-wake/)).toHaveTextContent(
		"turn closed-lid wake off in every AO instance and wait for recovery to finish",
	);
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
});

test("turning off Keep awake clears closed-lid mode in the same request", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true, lidSupported: true, lidClosed: true };
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		Object.assign(mobileStatus.keepAwake!, { enabled: false, active: false, lidClosed: false, lidActive: false });
		return { data: structuredClone(mobileStatus), error: undefined };
	});
	renderMobileSettings();
	await userEvent.click(await screen.findByRole("switch", { name: "Keep this Mac awake" }));
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: false, lidClosed: false } });
	await waitFor(() => expect(screen.getByRole("switch", { name: "Keep awake with lid closed" })).not.toBeChecked());
	expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).not.toBeChecked();
});

test("turning off closed-lid mode keeps idle wake enabled", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true, lidSupported: true, lidClosed: true };
	renderMobileSettings();
	await userEvent.click(await screen.findByRole("switch", { name: "Keep awake with lid closed" }));
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: false } });
});

test("an inactive remembered lid choice is unchecked and can enable both modes", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: true, lidSupported: true, lidClosed: true };
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep awake with lid closed" });
	expect(toggle).not.toBeChecked();
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
	await userEvent.click(toggle);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: true } });
});

test("enabling idle wake never restores an unchecked legacy lid choice", async () => {
	mobileStatus.keepAwake = {
		supported: true, enabled: false, active: false, hasBattery: true,
		lidSupported: true, lidClosed: true, lidActive: false,
	};
	let finishPost: (() => void) | undefined;
	vi.mocked(apiClient.POST).mockImplementationOnce(() => new Promise((resolve) => {
		finishPost = () => {
			Object.assign(mobileStatus.keepAwake!, { enabled: true, active: true, lidClosed: false });
			resolve({ data: structuredClone(mobileStatus), error: undefined });
		};
	}));
	renderMobileSettings();
	const mainToggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	const lidToggle = screen.getByRole("switch", { name: "Keep awake with lid closed" });
	expect(lidToggle).not.toBeChecked();
	await userEvent.click(mainToggle);
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", {
		body: { enabled: true, lidClosed: false },
	}));
	// The optimistic state must not flash lid mode on while the POST is pending.
	expect(mainToggle).toBeChecked();
	expect(lidToggle).not.toBeChecked();
	expect(lidToggle).toBeDisabled();
	finishPost!();
	await waitFor(() => expect(lidToggle).not.toBeDisabled());
	expect(mainToggle).toBeChecked();
	expect(lidToggle).not.toBeChecked();
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
});

// Settings prefetches the status, so opening the page reads it from the cache:
// Keep awake and the code are in their final state in the very first render.
test("renders cached state on first paint, with no transition on the code", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: false };
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(mobileStatusQueryKey, structuredClone(mobileStatus));
	renderMobileSettings(client);

	const toggle = screen.getByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).toBeChecked();
	expect(toggle).toBeEnabled();
	expect(screen.getByTestId("mobile-pairing-address")).toHaveTextContent("192.168.1.42:3011");
	expect(document.querySelector("[data-qr-value]")?.parentElement).toHaveClass("opacity-100", "transition-none");

	// Regenerating is the user's doing, so that change crossfades in: the new
	// code ends up fully shown on its own, and the old one is cleared.
	const before = qrPayload();
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		mobileStatus.password = "regenerated-password";
		return { data: structuredClone(mobileStatus), error: undefined } as never;
	});
	await userEvent.click(screen.getByRole("button", { name: "Regenerate password" }));
	await waitFor(() => expect(qrPayload()).not.toBe(before));
	await waitFor(() => {
		expect(document.querySelector("[data-qr-value]")?.parentElement).toHaveClass("opacity-100");
		expect(document.querySelectorAll("[data-qr-value]")).toHaveLength(1);
	});
});

// A cold open (nothing cached yet) keeps the row in its last-known state,
// disabled until the daemon answers, instead of popping it in afterwards.
test("holds Keep awake at its last-known state while the status loads", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: false };
	window.localStorage.setItem("ao.mobile.keepAwake", JSON.stringify({ supported: true, enabled: true, hasBattery: false }));
	renderMobileSettings();

	const pending = screen.getByRole("switch", { name: "Keep this Mac awake" });
	expect(pending).toBeChecked();
	expect(pending).toBeDisabled();
	await waitFor(() => expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toBeEnabled());
	expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toBeChecked();
});
