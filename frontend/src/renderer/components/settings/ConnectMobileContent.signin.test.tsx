import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { apiClient } from "../../lib/api-client";
import { AccountHostSyncError, deleteAccountRemoteHost } from "../../lib/account-remote-hosts";
import { linkThisComputer } from "../../lib/link-this-computer";
import { ThisComputerAccess } from "./ThisComputerAccess";

// Remote access away from home needs sign-in. The daemon fake is just enough
// to start the bridge: enable turns it on with its tunnel, and remote-access
// asks for one.
const fake = vi.hoisted(() => ({
	signedIn: false,
	signIn: vi.fn(),
	failRemoteAccess: false,
	// The quick tunnel's name once remote access starts; "" starts none.
	tunnelHost: "",
	status: {
		enabled: false,
		noPublicTunnel: false,
		host: "192.168.1.42",
		tailscaleHost: "",
		port: 3011,
		password: "",
		hostId: "h_fixture",
		endpoints: [] as Array<{ kind: string; host: string; port: number; secure: boolean }>,
		warning: "",
		securePairing: { enabled: false, available: false, active: false, host: "", port: 0, reason: "" },
	},
}));

vi.mock("../../hooks/useAwake", () => ({
	useAwake: () => ({ state: { sessions: [], rules: [], capabilities: [{ id: "idle", available: true }] }, actAsync: vi.fn(), busy: false }),
	awakeCapability: () => ({ available: true }),
}));
vi.mock("../../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../../lib/cloud-session", () => ({
	useCloudSession: () => ({
		session: fake.signedIn ? { user: { id: "user_01JAO" } } : null,
		status: fake.signedIn ? "authenticated" : "unauthenticated",
		configured: true,
		signIn: fake.signIn,
	}),
}));
vi.mock("../../hooks/useCloudLocalAuth", () => ({ useCloudLocalAuth: () => ({ available: false }) }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.example.test" } }) }));
let listedHosts = new Set<string>();

vi.mock("../../lib/account-remote-hosts", async (original) => ({
	...(await original<typeof import("../../lib/account-remote-hosts")>()),
	listAccountRemoteHosts: vi.fn(async () => [...listedHosts].map((hostId) => ({ hostId, label: "Mac", url: "" }))),
	readAccountHostDirectory: vi.fn(async () => ({
		hosts: [...listedHosts].map((hostId) => ({ hostId, label: "Mac", url: "" })),
		fixedAddresses: true,
		hostKeys: true,
	})),
	deleteAccountRemoteHost: vi.fn(async () => undefined),
}));
vi.mock("../../lib/mobile-pairing-code", () => ({ mobilePairingCodeQueryKey: ["mobile-pairing-code"], PAIRING_CODE_REFRESH_MS: 540_000, fetchPairingCode: async () => ({ code: "Code1234", expiresAt: Date.now() + 600_000 }) }));
vi.mock("../../lib/link-this-computer", () => ({ linkThisComputer: vi.fn(async () => ({ hostId: "h_fixture", added: true })) }));
vi.mock("../../lib/api-client", () => ({
	apiClient: {
		GET: async (path: string) => (path === "/api/v1/mobile/devices" ? { data: { devices: [] } } : { data: structuredClone(fake.status) }),
		POST: vi.fn(async (path: string, init?: { body?: { enabled?: boolean } }) => {
			if (path === "/api/v1/mobile/public-tunnel") fake.status.noPublicTunnel = init?.body?.enabled === false;
			if (path === "/api/v1/mobile/enable-lan-only") {
				Object.assign(fake.status, {
					enabled: true, noPublicTunnel: true, password: "pw-1",
					endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
				});
			}
			if (path === "/api/v1/mobile/enable") {
				Object.assign(fake.status, {
					enabled: true, noPublicTunnel: false, password: "pw-1",
					endpoints: [
						{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
						...(fake.tunnelHost ? [{ kind: "tunnel", host: fake.tunnelHost, port: 443, secure: true }] : []),
					],
				});
			}
			if (path === "/api/v1/mobile/remote-access") {
				if (fake.failRemoteAccess) return { error: { error: { message: "connector failed" } } };
				fake.status.noPublicTunnel = false;
				if (fake.tunnelHost && !fake.status.endpoints.some((e) => e.kind === "tunnel")) fake.status.endpoints.push({ kind: "tunnel", host: fake.tunnelHost, port: 443, secure: true });
			}
			if (path === "/api/v1/mobile/regenerate") fake.status.password = "pw-2";
			return { data: structuredClone(fake.status) };
		}),
	},
	apiErrorMessage: () => "failed",
}));

import { ConnectMobileContent } from "./ConnectMobileContent";

function renderContent() {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider>
				<ConnectMobileContent active />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	listedHosts.clear();
	Object.assign(fake.status, { enabled: false, noPublicTunnel: false, password: "", endpoints: [] });
	vi.mocked(apiClient.POST).mockClear();
	// Reset, not cleared: a rejection a test queued and never used must not
	// reach the next test.
	vi.mocked(linkThisComputer).mockReset().mockImplementation(async () => ({ hostId: "h_fixture", added: true }));
	vi.mocked(deleteAccountRemoteHost).mockClear();
	fake.failRemoteAccess = false;
	fake.tunnelHost = "";
});

afterEach(() => {
	vi.unstubAllEnvs();
});

const posted = () => (vi.mocked(apiClient.POST).mock.calls as unknown[][]).map(([path]) => path);

test("a signed-out computer pairs with a quick tunnel, as before, and hears what signing in adds", async () => {
	fake.signedIn = false;
	fake.tunnelHost = "quiet-river.trycloudflare.com";
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	expect(await screen.findByTestId("mobile-sign-in-hint")).toHaveTextContent("address that stays the same after restarts");
	expect(posted()).toEqual(["/api/v1/mobile/enable"]);
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a signed-in computer starts as signed out, then is linked and asks for its fixed address", async () => {
	fake.signedIn = true;
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	// enable first: an app that quits during the link leaves the quick tunnel, not a LAN-only computer.
	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/enable", "/api/v1/mobile/remote-access"]));
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-1", undefined);
	expect(vi.mocked(linkThisComputer).mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(apiClient.POST).mock.invocationCallOrder[1]);
	expect(screen.queryByTestId("mobile-sign-in-hint")).toBeNull();
});

test("a listing this enable added is taken back when remote access fails", async () => {
	fake.signedIn = true;
	fake.failRemoteAccess = true;
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	await waitFor(() => expect(deleteAccountRemoteHost).toHaveBeenCalledWith("https://cp.example.test", "h_fixture"));
});

test("a computer AO Cloud could not give a fixed address says why and is not listed elsewhere", async () => {
	fake.signedIn = true;
	fake.tunnelHost = "quick.trycloudflare.com";
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("The fixed address could not be prepared.", 502));
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	expect(await screen.findByText("The fixed address could not be prepared.")).toBeVisible();
	await new Promise((resolve) => setTimeout(resolve, 100));
	expect(linkThisComputer).toHaveBeenCalledTimes(1);
});

test("a new password links the computer again, so its fixed address survives", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: false, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	// Only while the account still lists it: one removed from the account is not listed again.
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true }));
	expect(posted()).toEqual(["/api/v1/mobile/regenerate"]);
});

test("a new password that could not be linked says so", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: false, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("AO Cloud host sync failed (502).", 502));
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	expect(await screen.findByText("AO Cloud host sync failed (502).")).toBeVisible();
});

test("an account that uses all of its fixed addresses lists a new password at the tunnel's address", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: false, password: "pw-1",
		endpoints: [
			{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
			{ kind: "tunnel", host: "quiet-river.trycloudflare.com", port: 443, secure: true },
		],
	});
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("AO Cloud host sync failed (409).", 409));
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith(
		"https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true, url: "https://quiet-river.trycloudflare.com:443" },
	));
	expect(screen.queryByText("AO Cloud host sync failed (409).")).toBeNull();
});

test("on an AO Cloud without fixed addresses a new password is listed at the tunnel's address", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: false, password: "pw-1",
		endpoints: [
			{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
			{ kind: "tunnel", host: "quiet-river.trycloudflare.com", port: 443, secure: true },
		],
	});
	vi.mocked(linkThisComputer).mockResolvedValueOnce({ hostId: "", added: false });
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith(
		"https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true, url: "https://quiet-river.trycloudflare.com:443" },
	));
});

test("with Reach from anywhere off, a signed-in computer pairs on this network and Tailscale only", async () => {
	fake.signedIn = true;
	fake.tunnelHost = "quiet-river.trycloudflare.com";
	renderContent();
	const reach = await screen.findByRole("switch", { name: "Reach from anywhere" });
	expect(reach).toBeChecked();
	await userEvent.click(reach);
	await waitFor(() => expect(reach).not.toBeChecked());
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/public-tunnel", "/api/v1/mobile/enable-lan-only"]));
	expect(vi.mocked(apiClient.POST).mock.calls[0]).toEqual(["/api/v1/mobile/public-tunnel", { body: { enabled: false } }]);
	expect(linkThisComputer).not.toHaveBeenCalled();
	expect(fake.status.endpoints.map((e) => e.kind)).toEqual(["lan"]);
	expect(screen.queryByTestId("mobile-sign-in-hint")).toBeNull();
});

test("turning Reach from anywhere back on links the computer and starts its tunnel", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: true, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	renderContent();
	const reach = await screen.findByRole("switch", { name: "Reach from anywhere" });
	expect(reach).not.toBeChecked();
	await userEvent.click(reach);

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/remote-access"]));
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-1", undefined);
	await waitFor(() => expect(reach).toBeChecked());
});

test("a LAN-only computer gets a new password without being listed", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: true, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/regenerate"]));
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a signed-out computer gets a new password without linking", async () => {
	fake.signedIn = false;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: true, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/regenerate"]));
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a development build starts remote access without publishing its temporary identity", async () => {
	vi.stubEnv("MODE", "development");
	fake.signedIn = true;
	vi.mocked(linkThisComputer).mockImplementation(async (_baseUrl, _accountId, hostId, _pw, options) => {
		if (options?.ifListed && !listedHosts.has(hostId)) return { hostId: "", added: false };
		const added = !listedHosts.has(hostId);
		listedHosts.add(hostId);
		return { hostId, added };
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/enable", "/api/v1/mobile/remote-access"]));
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-1", { ifListed: true });
	expect(listedHosts.has("h_fixture")).toBe(false);
});

test("a development build that is already listed relinks its listing when remote access is enabled", async () => {
	vi.stubEnv("MODE", "development");
	fake.signedIn = true;
	listedHosts.add("h_fixture");
	vi.mocked(linkThisComputer).mockImplementation(async (_baseUrl, _accountId, hostId, _pw, options) => {
		if (options?.ifListed && !listedHosts.has(hostId)) return { hostId: "", added: false };
		const added = !listedHosts.has(hostId);
		listedHosts.add(hostId);
		return { hostId, added };
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/enable", "/api/v1/mobile/remote-access"]));
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-1", { ifListed: true });
	const result = await vi.mocked(linkThisComputer).mock.results[0].value;
	expect(result).toEqual({ hostId: "h_fixture", added: false });
});

test("explicitly enabling Allow access in development, regenerating credentials, and refreshing the existing listing without creating a new one", async () => {
	vi.stubEnv("MODE", "development");
	fake.signedIn = true;
	fake.status.enabled = true;
	fake.status.password = "pw-1";
	fake.status.endpoints = [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }];

	listedHosts = new Set<string>();
	vi.mocked(linkThisComputer).mockImplementation(async (_baseUrl, _accountId, hostId, _pw, options) => {
		if (options?.ifListed && !listedHosts.has(hostId)) return { hostId: "", added: false };
		const added = !listedHosts.has(hostId);
		listedHosts.add(hostId);
		return { hostId, added };
	});

	// Before Allow access is turned on, regenerating in development does not create a listing.
	const unlistedContent = renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true }));
	expect(listedHosts.has("h_fixture")).toBe(false);
	unlistedContent.unmount();

	// Explicitly enabling Allow access in development creates the account listing.
	fake.tunnelHost = "quiet-river.trycloudflare.com";
	fake.status.enabled = false;
	fake.status.password = "";
	fake.status.endpoints = [];
	const access = render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<ThisComputerAccess />
		</QueryClientProvider>,
	);
	const toggle = await screen.findByRole("switch", { name: "Allow access from your devices" });
	await userEvent.click(toggle);
	await waitFor(() => expect(listedHosts.has("h_fixture")).toBe(true));
	expect(linkThisComputer).toHaveBeenLastCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-1");
	access.unmount();

	// Regenerating credentials in development now refreshes the existing listing without creating a new one.
	vi.mocked(linkThisComputer).mockClear();
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true }));
	const result = await vi.mocked(linkThisComputer).mock.results[0].value;
	expect(result).toEqual({ hostId: "h_fixture", added: false });
});

test("a development build refreshes credentials at the tunnel's address when fixed addresses are unavailable", async () => {
	vi.stubEnv("MODE", "development");
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: false, password: "pw-1",
		endpoints: [
			{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
			{ kind: "tunnel", host: "quiet-river.trycloudflare.com", port: 443, secure: true },
		],
	});
	vi.mocked(linkThisComputer).mockResolvedValueOnce({ hostId: "", added: false });
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Revoke all devices" }));

	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith(
		"https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true, url: "https://quiet-river.trycloudflare.com:443" },
	));
});

