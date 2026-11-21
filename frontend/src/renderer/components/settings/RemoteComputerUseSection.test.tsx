import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), del: vi.fn() }));
const bridgeMocks = vi.hoisted(() => ({ setComputerUseTarget: vi.fn() }));
const hostMocks = vi.hoisted(() => ({ offers: {} as Record<string, boolean | "old"> }));
const savedMocks = vi.hoisted(() => ({ hosts: [] as { hostId: string; label: string; status: string }[] }));
const settingsMocks = vi.hoisted(() => ({ computerUse: true }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: apiMocks.get, PUT: apiMocks.put, DELETE: apiMocks.del },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		typeof error === "object" && error && "message" in error ? String((error as { message: unknown }).message) : fallback,
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { remotes: bridgeMocks } }));
vi.mock("../../lib/host-clients", () => ({
	clientForHost: (hostId: string) => ({
		GET: async () => {
			const offer = hostMocks.offers[hostId];
			// An older Orchestrator has no route.
			if (offer === "old" || offer === undefined) return { data: undefined, error: { code: "NOT_FOUND", message: "not found" } };
			return { data: { offered: offer, platform: "darwin" }, error: undefined };
		},
	}),
}));
vi.mock("../../lib/saved-remote-hosts", () => ({
	savedRemoteHosts: () => savedMocks.hosts,
	subscribeSavedRemoteHosts: () => () => undefined,
}));
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { computerUse: settingsMocks.computerUse }, isLoading: false }),
}));

import { RemoteComputerUseSection } from "./RemoteComputerUseSection";

let remote: { allowRemote: boolean; target?: { hostId: string; label: string; url: string } };

function renderSection(installed = true) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<RemoteComputerUseSection installed={installed} />
		</QueryClientProvider>,
	);
}

const runOnMenu = () => screen.findByRole("button", { name: "Run computer use on" });

beforeEach(() => {
	remote = { allowRemote: false };
	settingsMocks.computerUse = true;
	hostMocks.offers = {};
	savedMocks.hosts = [];
	apiMocks.get.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/computer-use/remote") return { data: remote, error: undefined };
		return { data: undefined, error: { message: `unexpected ${path}` } };
	});
	apiMocks.put.mockReset();
	apiMocks.del.mockReset();
	bridgeMocks.setComputerUseTarget.mockReset();
});

describe("Run computer use on", () => {
	it("lists this computer and the connected computers that offer computer use", async () => {
		savedMocks.hosts = [
			{ hostId: "mac", label: "Studio Mac", status: "connected" },
			{ hostId: "pc", label: "Gaming PC", status: "connected" },
			{ hostId: "old", label: "Old laptop", status: "connected" },
			{ hostId: "away", label: "Away laptop", status: "offline" },
		];
		hostMocks.offers = { mac: true, pc: false, old: "old", away: true };
		renderSection();

		await userEvent.click(await runOnMenu());
		const menu = await screen.findByRole("menu");
		await waitFor(() => expect(within(menu).getByRole("menuitem", { name: /Studio Mac/ })).toBeInTheDocument());
		expect(within(menu).getByRole("menuitem", { name: /This computer/ })).toBeInTheDocument();
		for (const hidden of ["Gaming PC", "Old laptop", "Away laptop"]) {
			expect(within(menu).queryByRole("menuitem", { name: new RegExp(hidden) })).not.toBeInTheDocument();
		}
	});

	it("hands the choice to the desktop app, which gives the daemon the credential", async () => {
		savedMocks.hosts = [{ hostId: "mac", label: "Studio Mac", status: "connected" }];
		hostMocks.offers = { mac: true };
		bridgeMocks.setComputerUseTarget.mockImplementation(async () => {
			remote = { allowRemote: false, target: { hostId: "mac", label: "Studio Mac", url: "https://h-mac.useao.dev" } };
			return remote;
		});
		renderSection();

		await userEvent.click(await runOnMenu());
		await userEvent.click(await screen.findByRole("menuitem", { name: /Studio Mac/ }));
		expect(bridgeMocks.setComputerUseTarget).toHaveBeenCalledWith("mac");
		await waitFor(async () => expect(await runOnMenu()).toHaveTextContent("Studio Mac"));
	});

	it("runs computer use here again", async () => {
		savedMocks.hosts = [{ hostId: "mac", label: "Studio Mac", status: "connected" }];
		hostMocks.offers = { mac: true };
		remote = { allowRemote: false, target: { hostId: "mac", label: "Studio Mac", url: "https://h-mac.useao.dev" } };
		apiMocks.del.mockImplementation(async () => {
			remote = { allowRemote: false };
			return { data: remote, error: undefined };
		});
		renderSection();

		await waitFor(async () => expect(await runOnMenu()).toHaveTextContent("Studio Mac"));
		await userEvent.click(await runOnMenu());
		await userEvent.click(await screen.findByRole("menuitem", { name: /This computer/ }));
		expect(apiMocks.del).toHaveBeenCalledWith("/api/v1/computer-use/remote/target");
		await waitFor(async () => expect(await runOnMenu()).toHaveTextContent("This computer"));
	});

	it("keeps showing a chosen computer that stopped offering computer use, and says calls will fail", async () => {
		savedMocks.hosts = [{ hostId: "mac", label: "Studio Mac", status: "connected" }];
		hostMocks.offers = { mac: false };
		remote = { allowRemote: false, target: { hostId: "mac", label: "Studio Mac", url: "https://h-mac.useao.dev" } };
		renderSection();

		expect(await screen.findByText(/Studio Mac is not offering computer use right now/)).toBeInTheDocument();
		expect(await runOnMenu()).toHaveTextContent("Studio Mac");
	});

	it("shows why a choice failed", async () => {
		savedMocks.hosts = [{ hostId: "mac", label: "Studio Mac", status: "connected" }];
		hostMocks.offers = { mac: true };
		bridgeMocks.setComputerUseTarget.mockRejectedValue(new Error("Studio Mac did not answer."));
		renderSection();

		await userEvent.click(await runOnMenu());
		await userEvent.click(await screen.findByRole("menuitem", { name: /Studio Mac/ }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Studio Mac did not answer.");
	});

	it("points at Remote hosts when there are no other computers", async () => {
		renderSection();
		expect(await screen.findByText(/Connect your other computers under Remote hosts/)).toBeInTheDocument();
	});
});

describe("Let agents on your other computers use this computer", () => {
	const allowSwitch = () => screen.findByRole("switch", { name: "Let agents on your other computers use this computer" });

	it("turns on through the daemon", async () => {
		apiMocks.put.mockImplementation(async (_path: string, { body }: { body: { enabled: boolean } }) => {
			remote = { allowRemote: body.enabled };
			return { data: remote, error: undefined };
		});
		renderSection();
		await waitFor(async () => expect(await allowSwitch()).toBeEnabled());
		await userEvent.click(await allowSwitch());
		expect(apiMocks.put).toHaveBeenCalledWith("/api/v1/computer-use/remote/allow", { body: { enabled: true } });
		await waitFor(async () => expect(await allowSwitch()).toBeChecked());
	});

	it("needs Cua Driver to turn on", async () => {
		renderSection(false);
		await waitFor(async () => expect(await allowSwitch()).toBeDisabled());
		expect(screen.getByText(/can read its screen and operate its apps/)).toBeInTheDocument();
	});

	it("warns while agent computer use is off here", async () => {
		remote = { allowRemote: true };
		settingsMocks.computerUse = false;
		renderSection();
		expect(await screen.findByText(/Also turn on "Let agents use this computer" above/)).toBeInTheDocument();
	});
});
