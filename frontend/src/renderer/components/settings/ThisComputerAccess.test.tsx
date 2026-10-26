import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// The two network edges are faked; the account sync code between them is real.
// The daemon fake follows BridgeService: enable mints a new password, the
// quick tunnel gets its address a few status reads later, enable-lan-only stops
// the tunnel but keeps a running bridge's password, and the account token needs
// the current password. The cloud fake follows remote_host_handlers.
const fake = vi.hoisted(() => {
	const HOST_ID = "h_6f1c2b9e-0d4a-4c1e-9b7a-3f2d1e0c9b8a";
	const TOKEN = "0f".repeat(32);
	const TUNNEL = "quiet-river.trycloudflare.com";
	const daemon = {
		enabled: false,
		password: "",
		noPublicTunnel: false,
		connector: true,
		tunnelHost: "",
		tunnelError: "",
		readsUntilTunnel: 0,
		passwordsIssued: 0,
	};
	const cloud = {
		hosts: new Map<string, { label: string; url: string; token: string }>(),
		failPut: false,
		failDelete: false,
	};
	const log: string[] = [];
	const tokenRequests: Array<{ authorization: string; account: string }> = [];
	const snapshot = () => {
		const ready = daemon.enabled && daemon.tunnelHost !== "";
		return {
			enabled: daemon.enabled,
			loopbackOnly: false,
			noPublicTunnel: daemon.noPublicTunnel,
			endpoints: daemon.enabled
				? [
					{ kind: "lan", host: "192.168.1.20", port: 3011, secure: false },
					...(ready ? [{ kind: "tunnel", host: daemon.tunnelHost, port: 443, secure: true }] : []),
				]
				: [],
			hostId: HOST_ID,
			tunnel: {
				supported: daemon.connector,
				running: daemon.enabled && daemon.connector && !daemon.noPublicTunnel,
				ready,
				hostname: ready ? daemon.tunnelHost : "",
				location: "",
				lastError: daemon.tunnelError,
			},
			host: "192.168.1.20",
			tailscaleHost: "",
			port: daemon.enabled ? 3011 : 0,
			password: daemon.enabled ? daemon.password : "",
			warning: "Traffic on this connection is not encrypted. Only use it on a network you trust.",
			securePairing: { enabled: false, available: false, active: false, host: "", port: 0, reason: "" },
			keepAwake: { supported: true, enabled: false, active: false, hasBattery: false },
		};
	};
	const readStatus = () => {
		if (daemon.enabled && daemon.connector && !daemon.noPublicTunnel && !daemon.tunnelError && !daemon.tunnelHost) {
			if (daemon.readsUntilTunnel > 0) daemon.readsUntilTunnel--;
			else daemon.tunnelHost = TUNNEL;
		}
		return snapshot();
	};
	const post = (path: string, init?: { headers?: Record<string, string> }) => {
		log.push(path);
		switch (path) {
			case "/api/v1/mobile/enable":
				daemon.enabled = true;
				daemon.password = `pw-${++daemon.passwordsIssued}`;
				daemon.noPublicTunnel = false;
				return { data: snapshot() };
			case "/api/v1/mobile/remote-access":
				if (daemon.enabled) daemon.noPublicTunnel = false;
				return { data: snapshot() };
			case "/api/v1/mobile/enable-lan-only":
				daemon.tunnelHost = "";
				if (!daemon.enabled) {
					daemon.enabled = true;
					daemon.password = `pw-${++daemon.passwordsIssued}`;
				}
				daemon.noPublicTunnel = true;
				return { data: snapshot() };
			case "/api/v1/mobile/disable":
				daemon.enabled = false;
				daemon.tunnelHost = "";
				return { data: undefined };
			case "/api/v1/remote-host/account-token": {
				const authorization = init?.headers?.Authorization ?? "";
				tokenRequests.push({ authorization, account: init?.headers?.["X-AO-Account-ID"] ?? "" });
				if (!daemon.enabled || authorization !== `Bearer ${daemon.password}`) {
					return { error: { error: { code: "forbidden", message: "connection password required" } } };
				}
				return { data: { hostId: HOST_ID, token: TOKEN } };
			}
			default:
				return { error: { error: { code: "not_found", message: `unexpected POST ${path}` } } };
		}
	};
	// The desktop sends AO Cloud calls through the main process (window.ao.cloudCp).
	const cloudRequest = async ({ path, method, body }: { path: string; method: string; body?: string }) => {
		const reply = (status: number, json?: unknown) => ({ status, headers: { "content-type": "application/json" }, body: json === undefined ? "" : JSON.stringify(json) });
		const match = /^\/api\/cloud\/v1\/me\/hosts(?:\/([^/?]+))?/.exec(path);
		if (!match) return reply(404);
		const hostId = match[1] ? decodeURIComponent(match[1]) : "";
		if (method === "GET") {
			return reply(200, {
				hosts: [...cloud.hosts].map(([id, host]) => ({ hostId: id, ...host, updatedAt: "2026-10-19T08:00:00Z" })),
			});
		}
		log.push(`cloud ${method} ${hostId}`);
		if (method === "PUT") {
			const input = JSON.parse(body ?? "{}") as { label: string; url: string; token: string };
			if (cloud.failPut) return reply(503);
			const labelBytes = new TextEncoder().encode(input.label).length;
			if (labelBytes === 0 || labelBytes > 64 || !/^[0-9a-f]{64}$/.test(input.token)) return reply(422);
			cloud.hosts.set(hostId, input);
			return reply(204);
		}
		if (method === "DELETE") {
			if (cloud.failDelete) return reply(503);
			cloud.hosts.delete(hostId);
			return reply(204);
		}
		return reply(405);
	};
	return { HOST_ID, TOKEN, TUNNEL, daemon, cloud, log, tokenRequests, readStatus, post, cloudRequest, computerName: "Dhruv’s Mac mini" };
});

vi.mock("../../lib/api-client", () => ({
	apiClient: {
		GET: vi.fn(async (path: string) =>
			path === "/api/v1/mobile/status" ? { data: fake.readStatus() } : { error: { error: { message: `unexpected GET ${path}` } } }),
		POST: vi.fn(async (path: string, init?: { headers?: Record<string, string> }) => fake.post(path, init)),
	},
	apiErrorMessage: (error: unknown) => (error as { error?: { message?: string } } | undefined)?.error?.message ?? "Request failed",
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { remotes: { computerName: async () => fake.computerName } } }));
vi.mock("../../lib/cloud-session", () => ({
	useCloudSession: () => ({ status: "authenticated", session: { user: { id: "user_01JAO" } }, signIn: vi.fn() }),
}));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.example.test" } }) }));

import { ThisComputerAccess } from "./ThisComputerAccess";

const SWITCH = { name: "Allow access from your devices" };
type CloudBridge = { cloudCp: { request: typeof fake.cloudRequest } };
const cloudBridge = () => (window as unknown as { ao: CloudBridge }).ao.cloudCp;
const originalCloudRequest = cloudBridge().request;

const renderAccess = () =>
	render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<ThisComputerAccess />
		</QueryClientProvider>,
	);

async function readySwitch() {
	const toggle = await screen.findByRole("switch", SWITCH);
	await waitFor(() => expect(toggle).toBeEnabled());
	return toggle;
}

beforeEach(() => {
	cloudBridge().request = fake.cloudRequest;
});

afterEach(() => {
	vi.useRealTimers();
	cloudBridge().request = originalCloudRequest;
	Object.assign(fake.daemon, { enabled: false, password: "", noPublicTunnel: false, connector: true, tunnelHost: "", tunnelError: "", readsUntilTunnel: 0, passwordsIssued: 0 });
	fake.cloud.hosts.clear();
	fake.cloud.failPut = false;
	fake.cloud.failDelete = false;
	fake.log.length = 0;
	fake.tokenRequests.length = 0;
	fake.computerName = "Dhruv’s Mac mini";
});

it("links this computer to the account at its tunnel address when turned on", async () => {
	renderAccess();
	const toggle = await readySwitch();
	expect(toggle).toHaveAttribute("aria-checked", "false");

	fireEvent.click(toggle);

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "true");
	expect(fake.cloud.hosts.get(fake.HOST_ID)).toEqual({
		label: "Dhruv’s Mac mini",
		url: "https://quiet-river.trycloudflare.com:443",
		token: fake.TOKEN,
	});
	expect(fake.tokenRequests).toEqual([{ authorization: "Bearer pw-1", account: "user_01JAO" }]);
});

it("keeps a paired phone's password by starting remote access instead of re-enabling", async () => {
	Object.assign(fake.daemon, { enabled: true, password: "pw-phone", noPublicTunnel: true });
	renderAccess();
	fireEvent.click(await readySwitch());

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(fake.log).not.toContain("/api/v1/mobile/enable");
	expect(fake.daemon.password).toBe("pw-phone");
	expect(fake.tokenRequests).toEqual([{ authorization: "Bearer pw-phone", account: "user_01JAO" }]);
});

it("waits for the tunnel address before linking", async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	fake.daemon.readsUntilTunnel = 3;
	renderAccess();
	fireEvent.click(await readySwitch());

	expect(await screen.findByText("Starting remote access…")).toBeVisible();
	expect(fake.cloud.hosts.size).toBe(0);

	await act(async () => {
		await vi.advanceTimersByTimeAsync(10_000);
	});

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(fake.cloud.hosts.get(fake.HOST_ID)?.url).toBe("https://quiet-river.trycloudflare.com:443");
});

it("gives up with an error when the tunnel never gets an address", async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	fake.daemon.readsUntilTunnel = Number.POSITIVE_INFINITY;
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);
	expect(await screen.findByText("Starting remote access…")).toBeVisible();

	await act(async () => {
		await vi.advanceTimersByTimeAsync(120_000);
	});

	expect(await screen.findByRole("alert")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "false");
	expect(fake.cloud.hosts.size).toBe(0);
	expect(fake.daemon.enabled).toBe(false);
});

it("turns off by cutting access and removing this computer from the account", async () => {
	Object.assign(fake.daemon, { enabled: true, password: "pw-1", tunnelHost: fake.TUNNEL });
	fake.cloud.hosts.set(fake.HOST_ID, { label: "Dhruv’s Mac mini", url: "https://quiet-river.trycloudflare.com:443", token: fake.TOKEN });
	renderAccess();
	const toggle = await readySwitch();
	await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "true"));

	fireEvent.click(toggle);

	await waitFor(() => expect(fake.cloud.hosts.size).toBe(0));
	expect(fake.daemon.enabled).toBe(false);
	await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
});

it("still cuts access when AO Cloud cannot remove this computer", async () => {
	Object.assign(fake.daemon, { enabled: true, password: "pw-1", tunnelHost: fake.TUNNEL });
	fake.cloud.hosts.set(fake.HOST_ID, { label: "Dhruv’s Mac mini", url: "https://quiet-river.trycloudflare.com:443", token: fake.TOKEN });
	fake.cloud.failDelete = true;
	renderAccess();
	const toggle = await readySwitch();
	await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "true"));

	fireEvent.click(toggle);

	expect(await screen.findByRole("alert")).toBeVisible();
	expect(fake.daemon.enabled).toBe(false);
	expect(toggle).toHaveAttribute("aria-checked", "false");
});

it("says why remote access is down while this computer stays linked", async () => {
	Object.assign(fake.daemon, { enabled: true, password: "pw-1", tunnelError: "cloudflared exited: network is unreachable" });
	fake.cloud.hosts.set(fake.HOST_ID, { label: "Dhruv’s Mac mini", url: "https://quiet-river.trycloudflare.com:443", token: fake.TOKEN });
	renderAccess();
	const toggle = await readySwitch();
	await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "true"));

	expect(await screen.findByRole("alert")).toHaveTextContent("cloudflared exited: network is unreachable");
	expect(screen.queryByText("Starting remote access…")).toBeNull();
});

it("offers to install cloudflared instead of linking when there is no connector", async () => {
	fake.daemon.connector = false;
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);

	expect(await screen.findByRole("button", { name: "Install cloudflared" })).toBeVisible();
	await waitFor(() => expect(fake.daemon.enabled).toBe(false));
	await waitFor(() => expect(toggle).toHaveAttribute("aria-checked", "false"));
	expect(fake.cloud.hosts.size).toBe(0);
});

it("shows off while remote access is off, even if the account still lists this computer", async () => {
	fake.cloud.hosts.set(fake.HOST_ID, { label: "Dhruv’s Mac mini", url: "https://quiet-river.trycloudflare.com:443", token: fake.TOKEN });
	renderAccess();
	const toggle = await readySwitch();
	// Let the account read land before judging the state.
	await act(async () => {
		await new Promise((resolve) => setTimeout(resolve, 50));
	});
	expect(fake.log).toEqual([]);
	expect(toggle).toHaveAttribute("aria-checked", "false");
});

it("reports a failed link and stays off", async () => {
	fake.cloud.failPut = true;
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);

	expect(await screen.findByRole("alert")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "false");
	expect(fake.cloud.hosts.size).toBe(0);
	expect(fake.daemon.enabled).toBe(false);
});

it("puts a paired phone's LAN-only access back when linking fails", async () => {
	Object.assign(fake.daemon, { enabled: true, password: "pw-phone", noPublicTunnel: true });
	fake.cloud.failPut = true;
	renderAccess();
	fireEvent.click(await readySwitch());

	expect(await screen.findByRole("alert")).toBeVisible();
	expect(fake.daemon).toMatchObject({ enabled: true, noPublicTunnel: true, password: "pw-phone", tunnelHost: "" });
});

it("leaves remote access that was already on running when linking fails", async () => {
	Object.assign(fake.daemon, { enabled: true, password: "pw-phone", tunnelHost: fake.TUNNEL });
	fake.cloud.failPut = true;
	renderAccess();
	fireEvent.click(await readySwitch());

	expect(await screen.findByRole("alert")).toBeVisible();
	expect(fake.daemon).toMatchObject({ enabled: true, noPublicTunnel: false, password: "pw-phone", tunnelHost: fake.TUNNEL });
});

it("shortens a long computer name to fit the account", async () => {
	fake.computerName = "Dhruv’s MacBook Pro — the one on the standing desk in the back office";
	renderAccess();
	fireEvent.click(await readySwitch());

	await waitFor(() => expect(fake.cloud.hosts.has(fake.HOST_ID)).toBe(true));
	const label = fake.cloud.hosts.get(fake.HOST_ID)?.label ?? "";
	expect(label.startsWith("Dhruv’s MacBook Pro — the one on the standing")).toBe(true);
	expect(new TextEncoder().encode(label).length).toBeLessThanOrEqual(64);
});
