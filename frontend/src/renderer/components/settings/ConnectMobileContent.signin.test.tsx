import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { apiClient } from "../../lib/api-client";
import { AccountHostSyncError, deleteAccountRemoteHost } from "../../lib/account-remote-hosts";
import { linkThisComputer } from "../../lib/link-this-computer";

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
vi.mock("../../lib/account-remote-hosts", async (original) => ({
	...(await original<typeof import("../../lib/account-remote-hosts")>()),
	deleteAccountRemoteHost: vi.fn(async () => undefined),
}));
vi.mock("../../lib/link-this-computer", () => ({ linkThisComputer: vi.fn(async () => ({ hostId: "h_fixture", added: true })) }));
vi.mock("../../lib/api-client", () => ({
	apiClient: {
		GET: async (path: string) => (path === "/api/v1/mobile/devices" ? { data: { devices: [] } } : { data: structuredClone(fake.status) }),
		POST: vi.fn(async (path: string) => {
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
	Object.assign(fake.status, { enabled: false, noPublicTunnel: false, password: "", endpoints: [] });
	vi.mocked(apiClient.POST).mockClear();
	// Reset, not cleared: a rejection a test queued and never used must not
	// reach the next test.
	vi.mocked(linkThisComputer).mockReset().mockImplementation(async () => ({ hostId: "h_fixture", added: true }));
	vi.mocked(deleteAccountRemoteHost).mockClear();
	fake.failRemoteAccess = false;
	fake.tunnelHost = "";
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
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

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
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

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
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

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
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith(
		"https://cp.example.test", "user_01JAO", "h_fixture", "pw-2", { ifListed: true, url: "https://quiet-river.trycloudflare.com:443" },
	));
});

test("a LAN-only computer gets a new password without being listed", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: true, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

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
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/regenerate"]));
	expect(linkThisComputer).not.toHaveBeenCalled();
});
