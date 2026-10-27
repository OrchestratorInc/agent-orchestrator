import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { apiClient } from "../../lib/api-client";
import { RemoteHostLimitError } from "../../lib/account-remote-hosts";
import { linkThisComputer } from "../../lib/link-this-computer";

// Remote access away from home needs sign-in. The daemon fake is just enough
// to start the bridge: enable-lan-only turns it on without a tunnel, and
// remote-access asks for one.
const fake = vi.hoisted(() => ({
	signedIn: false,
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
	useCloudSession: () => ({ session: fake.signedIn ? { user: { id: "user_01JAO" } } : null }),
}));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.example.test" } }) }));
vi.mock("../../lib/link-this-computer", () => ({ linkThisComputer: vi.fn(async () => "h_fixture") }));
vi.mock("../../lib/api-client", () => ({
	apiClient: {
		GET: async (path: string) => (path === "/api/v1/mobile/devices" ? { data: { devices: [] } } : { data: structuredClone(fake.status) }),
		POST: vi.fn(async (path: string) => {
			if (path === "/api/v1/mobile/enable-lan-only") {
				Object.assign(fake.status, {
					enabled: true, noPublicTunnel: true, password: "pw-1",
					endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
				});
			}
			if (path === "/api/v1/mobile/remote-access") fake.status.noPublicTunnel = false;
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
	vi.mocked(linkThisComputer).mockClear();
});

const posted = () => (vi.mocked(apiClient.POST).mock.calls as unknown[][]).map(([path]) => path);

test("a signed-out computer pairs on this network only and says why", async () => {
	fake.signedIn = false;
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	expect(await screen.findByTestId("mobile-sign-in-for-remote")).toHaveTextContent("Sign in to AO");
	expect(posted()).toEqual(["/api/v1/mobile/enable-lan-only"]);
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a signed-in computer is linked before its tunnel starts", async () => {
	fake.signedIn = true;
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	await waitFor(() => expect(posted()).toEqual(["/api/v1/mobile/enable-lan-only", "/api/v1/mobile/remote-access"]));
	expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "pw-1");
	expect(vi.mocked(linkThisComputer).mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(apiClient.POST).mock.invocationCallOrder[1]);
	expect(screen.queryByTestId("mobile-sign-in-for-remote")).toBeNull();
});

test("a full account is said, and remote access still starts", async () => {
	fake.signedIn = true;
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new RemoteHostLimitError("An account can list up to 10 machines. Remove one to add another."));
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Generate" }));

	expect(await screen.findByText("An account can list up to 10 machines. Remove one to add another.")).toBeVisible();
	expect(posted()).toEqual(["/api/v1/mobile/enable-lan-only", "/api/v1/mobile/remote-access"]);
});

test("a new password links the computer again, so its fixed address survives", async () => {
	fake.signedIn = true;
	Object.assign(fake.status, {
		enabled: true, noPublicTunnel: false, password: "pw-1",
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
	});
	renderContent();
	await userEvent.click(await screen.findByRole("button", { name: "Regenerate password" }));

	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01JAO", "pw-2"));
	expect(posted()).toEqual(["/api/v1/mobile/regenerate"]);
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
