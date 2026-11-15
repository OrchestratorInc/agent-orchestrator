import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { TooltipProvider } from "./ui/tooltip";
import { apiClient } from "../lib/api-client";
import { fetchPairingCode } from "../lib/mobile-pairing-code";
import { useUiStore } from "../stores/ui-store";

const { mobileStatus } = vi.hoisted(() => ({
	mobileStatus: {
		enabled: true,
		noPublicTunnel: false,
		host: "192.168.1.42",
		tailscaleHost: "100.72.46.7",
		port: 3011,
		password: "fake-password-for-testing",
		requireDeviceCredentials: false,
		hostId: "h_fixture",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
		tunnel: undefined as
			| undefined
			| {
					supported: boolean;
					installing?: boolean;
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

vi.mock("../hooks/useAwake", () => ({
	awakeQueryKey: ["awake"],
	useAwake: () => ({ state: { sessions: [], rules: [], capabilities: [{ id: "idle", available: true }] }, actAsync: vi.fn().mockResolvedValue({ active: true }), busy: false }),
	awakeCapability: () => ({ available: true }),
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../lib/mobile-pairing-code", () => {
	let made = 0;
	return {
		mobilePairingCodeQueryKey: ["mobile-pairing-code"],
		PAIRING_CODE_REFRESH_MS: 540_000,
		fetchPairingCode: vi.fn(async () => ({ code: `Next${++made}`, expiresAt: Date.now() + 600_000 })),
	};
});
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ session: null, status: "unauthenticated", configured: false, signIn: () => undefined }),
}));

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
	client.setQueryData(["mobile-pairing-code"], { code: "Code1234", expiresAt: Date.now() + 600_000 });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ConnectMobileContent active />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

function qrPayload(): string | null {
	const qr = document.querySelector("[data-qr-value]");
	return qr?.getAttribute("data-qr-value") ?? null;
}

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
	window.localStorage.clear();
	mobileStatus.enabled = true;
	mobileStatus.password = "fake-password-for-testing";
	mobileStatus.requireDeviceCredentials = false;
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
	useUiStore.setState({ settingsModal: null });
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
	expect(offer.token).toBe("Code1234");
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
		token: "Code1234",
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

	const regenerate = await screen.findByRole("button", { name: "Revoke all devices" });
	const row = regenerate.closest("dd");
	expect(row).toHaveClass("flex", "items-center");
	expect(row).toHaveTextContent("Code1234");
});

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
	const toggle = await screen.findByRole("switch", { name: "Connect mobile" });
	expect(toggle).toBeChecked();

	await userEvent.click(toggle);
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
		await userEvent.click(await screen.findByRole("switch", { name: "Connect mobile" }));
		await waitFor(() => expect(statusReads).toBeGreaterThan(1));
		await waitFor(() => {
			const toggle = screen.getByRole("switch", { name: "Connect mobile" });
			if (serverEnabled) {
				expect(toggle).toBeChecked();
				expect(qrPayload()).not.toBeNull();
			} else {
				expect(toggle).not.toBeChecked();
				expect(screen.getByRole("button", { name: "Generate" })).toBeVisible();
			}
		});
		if (failed) expect(screen.getByText("failed")).toBeVisible();
	} finally {
		get.mockRestore();
	}
});

test.skip("keeps the connection-method dropdown at its fixed width", async () => {
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	const control = screen.getByRole("button", { name: "Connection method" });
	expect(control).toHaveClass("w-44", "justify-between");

	await userEvent.click(control);
	expect(screen.getByRole("menu")).toHaveClass("!w-44", "!min-w-0");
});

test("does not tell LAN users to join the same Wi-Fi", async () => {
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.queryByText(/same Wi-Fi/i)).not.toBeInTheDocument();
	expect(screen.getByText("Scan with the Orchestrator mobile app")).toBeInTheDocument();
});

test("offers both store links without a platform choice", async () => {
	renderMobileSettings();

	expect(await screen.findByRole("button", { name: "Open Orchestrator on the App Store" })).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Open Orchestrator on Google Play" })).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Get the app" })).not.toBeInTheDocument();
});

test("shows the store links as one segmented control beside Get the app", async () => {
	renderMobileSettings();

	const ios = await screen.findByRole("button", { name: "Open Orchestrator on the App Store" });
	const android = screen.getByRole("button", { name: "Open Orchestrator on Google Play" });
	expect(ios.parentElement).toBe(android.parentElement);
	expect(ios.parentElement).toHaveClass("bg-(--color-bg-settings-input)");
	expect(ios.parentElement?.parentElement).toHaveTextContent("Get the app");
});

test("shows a square Google Play QR tooltip for Android", async () => {
	renderMobileSettings();
	await userEvent.hover(await screen.findByRole("button", { name: "Open Orchestrator on Google Play" }));

	expect(await screen.findByTestId("android-play-qr")).toHaveClass("p-2");
});

test("shows a QR-only App Store tooltip", async () => {
	renderMobileSettings();
	await userEvent.hover(await screen.findByRole("button", { name: "Open Orchestrator on the App Store" }));

	const tooltip = await screen.findByTestId("ios-store-qr");
	expect(tooltip).not.toHaveTextContent("App Store");
	expect(tooltip.querySelector("canvas")).toBeInTheDocument();
});

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
	expect(screen.queryByTestId("secure-pairing-reason")).not.toBeInTheDocument();
});

test("does not retry enabling secure pairing when it is unavailable", async () => {
	mobileStatus.securePairing = {
		enabled: false, available: false, active: false,
		host: "", port: 0, reason: "no_certs",
	};
	const { apiClient } = await import("../lib/api-client");
	vi.mocked(apiClient.POST).mockClear();
	renderMobileSettings();

	await waitFor(() => expect(screen.getByTestId("secure-pairing-reason")).toBeInTheDocument());
	expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/mobile/secure-pairing", { body: { enabled: true } });
});


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

test("keeps the connection token out of everything before the fragment", () => {
	const value = qrValueFor({
		hostId: "h_x", host: "192.168.1.42", platform: "darwin", password: "super-secret",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});

	expect(value.split("#")[0]).not.toContain("super-secret");
});

test("emits only v2, never a raw JSON v1 payload", () => {
	const value = qrValueFor({
		hostId: "h_x", host: "192.168.1.42", platform: "darwin", password: "pw",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});

	expect(value.startsWith("aomobile://pair#")).toBe(true);
	expect(() => JSON.parse(value)).toThrow();
	expect(decodeQr(value).v).toBe(2);
});

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

test("turns Connect Mobile on when opened to pair a phone", async () => {
	mobileStatus.enabled = false;
	act(() => useUiStore.getState().openGlobalSettings("mobile", { startPairing: true }));
	renderMobileSettings();
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/enable"));
	act(() => useUiStore.getState().closeSettings());
});

test("an opening that found Connect Mobile on leaves it off once switched off", async () => {
	act(() => useUiStore.getState().openGlobalSettings("mobile", { startPairing: true }));
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		mobileStatus.enabled = false;
		return { data: undefined, error: undefined } as never;
	});
	renderMobileSettings();
	await userEvent.click(await screen.findByRole("switch", { name: "Connect mobile" }));
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/disable"));
	await waitFor(() => expect(screen.getByRole("switch", { name: "Connect mobile" })).toHaveAttribute("aria-checked", "false"));
	await new Promise((resolve) => setTimeout(resolve, 50));
	expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/mobile/enable");
});

test("turns Connect Mobile on once per opening, not again when the page comes back", async () => {
	mobileStatus.enabled = false;
	act(() => useUiStore.getState().openGlobalSettings("mobile", { startPairing: true }));
	const first = renderMobileSettings();
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/enable"));
	first.unmount();
	renderMobileSettings();
	await screen.findByRole("button", { name: "Generate" });
	await new Promise((resolve) => setTimeout(resolve, 50));
	expect((vi.mocked(apiClient.POST).mock.calls as unknown[][]).filter(([path]) => path === "/api/v1/mobile/enable")).toHaveLength(1);
});

test("does not turn Connect Mobile on from a cached off status", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(mobileStatusQueryKey, { ...structuredClone(mobileStatus), enabled: false });
	act(() => useUiStore.getState().openGlobalSettings("mobile", { startPairing: true }));
	renderMobileSettings(client);
	await waitFor(() => expect(screen.getByRole("switch", { name: "Connect mobile" })).toHaveAttribute("aria-checked", "true"));
	expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/mobile/enable");
});

test("does not turn Connect Mobile on when browsed to", async () => {
	mobileStatus.enabled = false;
	act(() => useUiStore.getState().openGlobalSettings("mobile"));
	renderMobileSettings();
	await screen.findByRole("button", { name: "Generate" });
	expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/mobile/enable");
	act(() => useUiStore.getState().closeSettings());
});

test("says nothing under the code while remote access starts", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();
	await waitFor(() => expect(qrPayload()).not.toBeNull());

	expect(screen.queryByTestId("mobile-remote-access-on")).not.toBeInTheDocument();
	expect(screen.queryByTestId("mobile-local-only")).not.toBeInTheDocument();
	expect(screen.queryByText(/Enabling remote access|Until then/)).not.toBeInTheDocument();
});

test("keeps the same code element as the remote access note changes", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	renderMobileSettings(client);
	await waitFor(() => expect(qrPayload()).not.toBeNull());
	expect(screen.queryByTestId("mobile-remote-access-on")).not.toBeInTheDocument();
	const square = screen.getByTestId("mobile-pairing-panel").firstElementChild;
	const canvas = document.querySelector("[data-qr-value]");

	act(() => {
		client.setQueryData(mobileStatusQueryKey, {
			...structuredClone(mobileStatus),
			tunnel: { supported: true, running: false, ready: false, hostname: "", location: "", lastError: "connector exited" },
		});
	});
	await waitFor(() => expect(screen.getByTestId("mobile-local-only")).toBeInTheDocument());
	expect(screen.getByTestId("mobile-pairing-panel").firstElementChild).toBe(square);
	expect(document.querySelector("[data-qr-value]")).toBe(canvas);

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
	expect(localOnlyReason({ enabled: true, endpoints: lan, tunnel: starting })).toBeNull();
	expect(localOnlyReason({ enabled: true, endpoints: lan, tunnel: { ...starting, running: false, lastError: "boom" } })).toBe("failed");
	expect(localOnlyReason({ enabled: true, endpoints: lan, tunnel: { ...starting, supported: false, running: false } })).toBeNull();
	expect(localOnlyReason({ enabled: true, noPublicTunnel: true, endpoints: lan, tunnel: starting })).toBeNull();
	expect(localOnlyReason({ enabled: true, endpoints: [], tunnel: starting })).toBeNull();
	expect(localOnlyReason({ enabled: false, endpoints: lan, tunnel: starting })).toBeNull();
});

test("holds the QR back when nothing is reachable yet", () => {
	expect(qrIsReady({ enabled: true, endpoints: [], tunnel: undefined })).toBe(false);
});

test("withholds the QR from a daemon that reports no endpoints", () => {
	expect(qrIsReady({ enabled: true, endpoints: undefined, tunnel: undefined })).toBe(false);
	expect(qrIsReady({ enabled: true, endpoints: [], tunnel: undefined })).toBe(false);
});

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

test("polls while the daemon downloads cloudflared, so the connector shows when it lands", () => {
	const endpoints = [{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false }];
	expect(
		mobileStatusRefetchInterval({
			enabled: true,
			endpoints,
			tunnel: { supported: false, installing: true, running: false, ready: false },
		}),
	).toBeGreaterThan(0);
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

test("says so when this machine cannot be reached from elsewhere", async () => {
	mobileStatus.tunnel = {
		supported: false, running: false, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	expect(await screen.findByTestId("mobile-remote-unavailable")).toHaveTextContent(
		/only|cloudflared/i,
	);
});

test("does not claim cloudflared is missing while it downloads", async () => {
	mobileStatus.tunnel = {
		supported: false, installing: true, running: false, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	expect(await screen.findByTestId("mobile-install-cloudflared")).toBeVisible();
	expect(screen.queryByTestId("mobile-remote-unavailable")).toBeNull();
});

test("does not claim unavailability while the connector is merely starting", async () => {
	mobileStatus.tunnel = {
		supported: true, running: true, ready: false, hostname: "", location: "", lastError: "",
	};
	renderMobileSettings();

	await waitFor(() => expect(screen.queryByTestId("mobile-remote-unavailable")).toBeNull());
});

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

test.each([
	{ name: "absent", keepAwake: undefined },
	{ name: "unsupported", keepAwake: { supported: false, enabled: false, active: false, hasBattery: false } },
])("hides the keep-awake option when it is $name", async ({ keepAwake }) => {
	mobileStatus.keepAwake = keepAwake;
	renderMobileSettings();
	await screen.findByRole("switch", { name: "Connect mobile" });
	expect(screen.queryByTestId("mobile-keep-awake")).not.toBeInTheDocument();
});

test("turns the keep-awake option on", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: false, lidSupported: true, lidClosed: true };
	renderMobileSettings();

	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	expect(screen.queryByTestId("mobile-keep-awake-laptop")).not.toBeInTheDocument();

	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: false, lidSupported: true, lidClosed: true };
		return { data: structuredClone(mobileStatus), error: undefined } as never;
	});
	await userEvent.click(toggle);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: true } });
	expect(toggle).toBeChecked();
	await waitFor(() => expect(toggle).toBeChecked());
});

test("explains that the Mac keeps running with the lid closed", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true, lidSupported: true, lidClosed: true };
	renderMobileSettings();
	expect(await screen.findByRole("switch", { name: "Keep this Mac awake" })).toBeChecked();
	expect(screen.getByTestId("mobile-keep-awake")).toHaveTextContent("Keep your Mac running, even with the lid closed");
	expect(screen.queryByText(/Closing the lid still/)).not.toBeInTheDocument();
});

test("turning on closed-lid mode also enables idle wake in one request", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: true, lidSupported: true, lidClosed: false };
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		Object.assign(mobileStatus.keepAwake!, { enabled: true, lidClosed: true });
		return { data: structuredClone(mobileStatus), error: undefined };
	});
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	expect(screen.queryByRole("switch", { name: "Keep awake with lid closed" })).not.toBeInTheDocument();
	expect(screen.getByTestId("mobile-keep-awake")).toHaveTextContent("even with the lid closed");
	expect(screen.getByTestId("mobile-keep-awake")).not.toHaveTextContent("mobile access");
	expect(screen.getByTestId("mobile-keep-awake")).not.toHaveTextContent("Stops below");
	expect(screen.queryByTestId("mobile-keep-awake-laptop")).not.toBeInTheDocument();
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
	expect(await screen.findByRole("switch", { name: "Keep this Mac awake" })).toBeChecked();
	expect(screen.getByText(/Stopped below 10% battery/)).toBeInTheDocument();
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
});

test.each([undefined, "isolated_data_dir", "helper_missing"])("never falls back to idle-only wake when closed-lid support is unavailable (%s)", async (lidReason) => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true, lidReason };
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	expect(toggle).toBeDisabled();
	expect(screen.getByTestId("mobile-keep-awake")).toHaveTextContent("even with the lid closed");
	if (lidReason === "isolated_data_dir") {
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		expect(screen.queryByText("Requires Orchestrator’s default data folder.")).not.toBeInTheDocument();
		await userEvent.hover(screen.getByRole("button", { name: "About keeping this Mac awake" }));
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Closed-lid wakefulness is disabled for custom or isolated data directories.");
	} else {
		expect(screen.getByRole("status")).toHaveTextContent(lidReason === "helper_missing"
			? "The desktop wake helper is missing." : "Closed-lid wakefulness is unavailable on this Mac or macOS version.");
	}
	expect(screen.queryByText(/Closing the lid still/)).not.toBeInTheDocument();
	await userEvent.click(toggle);
	expect(apiClient.POST).not.toHaveBeenCalled();
});

test("keeps safety details accessible without filling the awake row", async () => {
	mobileStatus.enabled = false;
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: true, lidSupported: true };
	renderMobileSettings();
	expect(await screen.findByRole("switch", { name: "Keep this Mac awake" })).toBeEnabled();
	const details = screen.getByRole("button", { name: "About keeping this Mac awake" });
	await userEvent.hover(details);
	const tooltip = await screen.findByRole("tooltip");
	expect(tooltip).toHaveTextContent("Uses more battery. Keep your Mac ventilated.");
	expect(tooltip).toHaveTextContent("Stops below 10% battery or at critical heat.");
	expect(tooltip).not.toHaveTextContent("mobile access");
	expect(tooltip).not.toHaveTextContent("Closed-lid wakefulness is disabled for custom or isolated data directories.");
	expect(screen.getByTestId("mobile-keep-awake")).not.toHaveTextContent("custom or isolated");
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
	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).toHaveAttribute("aria-checked", String(lidClosed));
	expect(screen.getByText(/launchctl bootout gui\/\$\(id -u\)\/com\.aoagents\.mobile-wake/)).toHaveTextContent(
		"turn closed-lid wake off in every Orchestrator instance and wait for recovery to finish",
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
	await waitFor(() => expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).not.toBeChecked());
	expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).not.toBeChecked();
});

test("the combined switch upgrades an existing idle-only hold to closed-lid wake", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true, lidSupported: true, lidClosed: false };
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		Object.assign(mobileStatus.keepAwake!, { lidClosed: true });
		return { data: structuredClone(mobileStatus), error: undefined };
	});
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	await userEvent.click(toggle);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: true } });
	await waitFor(() => expect(toggle).toBeChecked());
});

test("an inactive remembered lid choice is unchecked and can enable both modes", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: false, active: false, hasBattery: true, lidSupported: true, lidClosed: true };
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
	await userEvent.click(toggle);
	expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", { body: { enabled: true, lidClosed: true } });
});

test("the combined switch stays checked and disabled while enabling both wake modes", async () => {
	mobileStatus.keepAwake = {
		supported: true, enabled: false, active: false, hasBattery: true,
		lidSupported: true, lidClosed: false, lidActive: false,
	};
	let finishPost: (() => void) | undefined;
	vi.mocked(apiClient.POST).mockImplementationOnce(() => new Promise((resolve) => {
		finishPost = () => {
			Object.assign(mobileStatus.keepAwake!, { enabled: true, active: true, lidClosed: true });
			resolve({ data: structuredClone(mobileStatus), error: undefined });
		};
	}));
	renderMobileSettings();
	const toggle = await screen.findByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).not.toBeChecked();
	await userEvent.click(toggle);
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/keep-awake", {
		body: { enabled: true, lidClosed: true },
	}));
	expect(toggle).toBeChecked();
	expect(toggle).toBeDisabled();
	finishPost!();
	await waitFor(() => expect(toggle).not.toBeDisabled());
	expect(toggle).toBeChecked();
	expect(screen.queryByText("Closed-lid keep awake is armed.")).not.toBeInTheDocument();
});

test("renders cached state on first paint, with no transition on the code", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: false, lidSupported: true, lidClosed: true };
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(mobileStatusQueryKey, structuredClone(mobileStatus));
	renderMobileSettings(client);

	const toggle = screen.getByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).toBeChecked();
	expect(toggle).toBeEnabled();
	expect(screen.getByTestId("mobile-pairing-address")).toHaveTextContent("192.168.1.42:3011");
	expect(document.querySelector("[data-qr-value]")?.parentElement).toHaveClass("opacity-100", "transition-none");

	const before = qrPayload();
	vi.mocked(apiClient.POST).mockImplementationOnce(async () => {
		mobileStatus.password = "regenerated-password";
		return { data: structuredClone(mobileStatus), error: undefined } as never;
	});
	await userEvent.click(screen.getByRole("button", { name: "Revoke all devices" }));
	await waitFor(() => expect(qrPayload()).not.toBe(before));
	await waitFor(() => {
		expect(document.querySelector("[data-qr-value]")?.parentElement).toHaveClass("opacity-100");
		expect(document.querySelectorAll("[data-qr-value]")).toHaveLength(1);
	});
});

test("holds Keep awake at its last-known state while the status loads", async () => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: false, lidSupported: true, lidClosed: true };
	window.localStorage.setItem("ao.mobile.keepAwake", JSON.stringify({ supported: true, enabled: true, hasBattery: false, lidSupported: true, lidClosed: true }));
	renderMobileSettings();

	const pending = screen.getByRole("switch", { name: "Keep this Mac awake" });
	expect(pending).toBeChecked();
	expect(pending).toBeDisabled();
	await waitFor(() => expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toBeEnabled());
	expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toBeChecked();
});


test.each([false, true])("remembers the combined lid preference %s during a cold load", async (lidClosed) => {
	mobileStatus.keepAwake = { supported: true, enabled: true, active: true, hasBattery: true, lidSupported: true, lidClosed };
	window.localStorage.setItem("ao.mobile.keepAwake", JSON.stringify(mobileStatus.keepAwake));
	renderMobileSettings();
	const toggle = screen.getByRole("switch", { name: "Keep this Mac awake" });
	expect(toggle).toHaveAttribute("aria-checked", String(lidClosed));
	expect(toggle).toBeDisabled();
	await waitFor(() => expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toBeEnabled());
	expect(screen.getByRole("switch", { name: "Keep this Mac awake" })).toHaveAttribute("aria-checked", String(lidClosed));
});

test("keeps the password hidden until asked for, and the QR never carries it", async () => {
	renderMobileSettings();
	const password = await screen.findByTestId("mobile-pairing-password");
	expect(password).toHaveTextContent("•".repeat("fake-password-for-testing".length));
	expect(document.body).not.toHaveTextContent("fake-password-for-testing");
	expect(qrPayload()).not.toContain("fake-password-for-testing");
	await userEvent.click(screen.getByRole("button", { name: "Show password" }));
	expect(password).toHaveTextContent("fake-password-for-testing");
	await userEvent.click(screen.getByRole("button", { name: "Hide password" }));
	expect(document.body).not.toHaveTextContent("fake-password-for-testing");
});

test("takes the password off the panel once the host refuses it", async () => {
	mobileStatus.requireDeviceCredentials = true;
	renderMobileSettings();
	expect(await screen.findByTestId("mobile-pairing-code")).toHaveTextContent("Code1234");
	expect(screen.queryByTestId("mobile-pairing-password")).not.toBeInTheDocument();
	expect(screen.queryByRole("switch", { name: /device credentials/i })).not.toBeInTheDocument();
});

test("never shows an expired pairing code", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	client.setQueryData(["mobile-pairing-code"], { code: "Expired1", expiresAt: Date.now() - 1 });
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ConnectMobileContent active />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	await waitFor(() => expect(screen.getByTestId("mobile-pairing-code")).toHaveTextContent(/^Next\d+$/));
	expect(document.body).not.toHaveTextContent("Expired1");
	expect(qrPayload()).not.toContain("Expired1");
});

test("offers to try again when no pairing code could be made", async () => {
	vi.mocked(fetchPairingCode).mockRejectedValueOnce(new Error("remote access is off; turn it on before pairing a device"));
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ConnectMobileContent active />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	await userEvent.click(await screen.findByTestId("mobile-pairing-code-retry"));
	await waitFor(() => expect(screen.getByTestId("mobile-pairing-code")).toHaveTextContent(/^Next\d+$/));
	expect(screen.queryByTestId("mobile-pairing-code-retry")).not.toBeInTheDocument();
});

test("drops a spent pairing code even when its replacement fails", async () => {
	let devices: Array<{ id: string; kind: string; live: boolean }> = [];
	const get = vi.spyOn(apiClient, "GET").mockImplementation(async (path) =>
		path === "/api/v1/mobile/devices" ? { data: { devices }, error: undefined } : { data: { ...mobileStatus }, error: undefined });
	try {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderMobileSettings(client);
		expect(await screen.findByTestId("mobile-pairing-code")).toHaveTextContent("Code1234");
		await waitFor(() => expect(client.getQueryData(["mobile-devices"])).toEqual([]));
		vi.mocked(fetchPairingCode).mockRejectedValueOnce(new Error("pairing code failed"));
		devices = [{ id: "dev_1", kind: "device", live: true }];
		await act(() => client.invalidateQueries({ queryKey: ["mobile-devices"] }));
		expect(await screen.findByTestId("mobile-pairing-code-retry")).toBeInTheDocument();
		expect(document.body).not.toHaveTextContent("Code1234");
	} finally {
		get.mockRestore();
	}
});
