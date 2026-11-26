import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// The two network edges are faked; the account sync code between them is real.
// The daemon fake follows BridgeService: enable mints a new password, the
// tunnel gets its address a few status reads later, enable-lan-only stops the
// tunnel but keeps a running bridge's password, and the account token needs
// the current password. A machine linked to the account serves its fixed
// address, an unlinked one a quick tunnel. The cloud fake follows
// remote_host_handlers: a host saved without a url is listed at its fixed
// address, and an AO Cloud without fixed addresses refuses that save.
const fake = vi.hoisted(() => {
	const HOST_ID = "h_6f1c2b9e-0d4a-4c1e-9b7a-3f2d1e0c9b8a";
	const TOKEN = "0f".repeat(32);
	const TUNNEL = "quiet-river.trycloudflare.com";
	const FIXED = "h-0d483066194a12a5dc17.useao.dev";
	const daemon = {
		enabled: false,
		password: "",
		noPublicTunnel: false,
		connector: true,
		tunnelHost: "",
		tunnelError: "",
		readsUntilTunnel: 0,
		passwordsIssued: 0,
		linked: false,
		// Status reads left while the daemon downloads its own cloudflared.
		downloadReads: 0,
	};
	const cloud = {
		hosts: new Map<string, { label: string; url: string; token: string }>(),
		failPut: false,
		failDelete: false,
		fixedAddresses: true,
		full: false,
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
				installing: !daemon.connector && daemon.downloadReads > 0,
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
		if (daemon.downloadReads > 0 && --daemon.downloadReads === 0) daemon.connector = true;
		if (daemon.enabled && daemon.connector && !daemon.noPublicTunnel && !daemon.tunnelError && !daemon.tunnelHost) {
			if (daemon.readsUntilTunnel > 0) daemon.readsUntilTunnel--;
			else daemon.tunnelHost = daemon.linked && cloud.hosts.has(HOST_ID) ? FIXED : TUNNEL;
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
				// A running connector moves to the fixed address a link just gave it.
				if (daemon.linked && cloud.hosts.has(HOST_ID) && daemon.tunnelHost !== FIXED) daemon.tunnelHost = "";
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
				daemon.linked = true;
				return { data: { hostId: HOST_ID, token: TOKEN, hostKey: "k".repeat(43) } };
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
				fixedAddresses: cloud.fixedAddresses,
			});
		}
		log.push(`cloud ${method} ${hostId}`);
		if (method === "PUT") {
			const input = JSON.parse(body ?? "{}") as { label: string; url?: string; token: string; hostKey?: string };
			// This AO Cloud lists no hostKeys, so it refuses the field, as an older one does.
			if (input.hostKey !== undefined) return reply(400);
			if (cloud.failPut) return reply(503);
			// An account that uses all its fixed addresses can still list more machines.
			if (cloud.full && input.url === undefined) return reply(409, { code: "fixed_address_limit_reached" });
			if (input.url === undefined && !cloud.fixedAddresses) return reply(422);
			input.url ??= `https://${FIXED}:443`;
			const labelBytes = new TextEncoder().encode(input.label).length;
			if (labelBytes === 0 || labelBytes > 64 || !/^[0-9a-f]{64}$/.test(input.token)) return reply(422);
			cloud.hosts.set(hostId, { ...input, url: input.url });
			return reply(204);
		}
		if (method === "DELETE") {
			if (cloud.failDelete) return reply(503);
			cloud.hosts.delete(hostId);
			return reply(204);
		}
		return reply(405);
	};
	return { HOST_ID, TOKEN, TUNNEL, FIXED, daemon, cloud, log, tokenRequests, readStatus, post, cloudRequest, computerName: "Dhruv’s Mac mini" };
});

vi.mock("../../lib/api-client", () => ({
	apiClient: {
		GET: vi.fn(async (path: string) => {
			if (path === "/api/v1/mobile/status") return { data: fake.readStatus() };
			if (path === "/api/v1/system/install/{target}") return { data: { target: "cloudflared", status: fake.daemon.downloadReads > 0 ? "running" : "idle" } };
			return { error: { error: { message: `unexpected GET ${path}` } } };
		}),
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
	Object.assign(fake.daemon, { enabled: false, password: "", noPublicTunnel: false, connector: true, tunnelHost: "", tunnelError: "", readsUntilTunnel: 0, passwordsIssued: 0, linked: false, downloadReads: 0 });
	fake.cloud.hosts.clear();
	fake.cloud.failPut = false;
	fake.cloud.failDelete = false;
	fake.cloud.fixedAddresses = true;
	fake.cloud.full = false;
	fake.log.length = 0;
	fake.tokenRequests.length = 0;
	fake.computerName = "Dhruv’s Mac mini";
});

it("links this computer once remote access is on, and its tunnel moves to its fixed address", async () => {
	renderAccess();
	const toggle = await readySwitch();
	expect(toggle).toHaveAttribute("aria-checked", "false");

	fireEvent.click(toggle);

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "true");
	expect(fake.cloud.hosts.get(fake.HOST_ID)).toEqual({ label: "Dhruv’s Mac mini", url: `https://${fake.FIXED}:443`, token: fake.TOKEN });
	expect(fake.daemon.tunnelHost).toBe(fake.FIXED);
	expect(fake.tokenRequests).toEqual([{ authorization: "Bearer pw-1", account: "user_01JAO" }]);
	expect(fake.log).toEqual([
		"/api/v1/mobile/enable",
		"/api/v1/remote-host/account-token",
		`cloud PUT ${fake.HOST_ID}`,
		"/api/v1/mobile/remote-access",
	]);
});

it("links at the quick tunnel's address when the account has no fixed address left, as before", async () => {
	fake.cloud.full = true;
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "true");
	expect(fake.cloud.hosts.get(fake.HOST_ID)?.url).toBe(`https://${fake.TUNNEL}:443`);
});

it("links at the quick tunnel's address when AO Cloud has no fixed addresses", async () => {
	fake.cloud.fixedAddresses = false;
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "true");
	expect(fake.cloud.hosts.get(fake.HOST_ID)?.url).toBe(`https://${fake.TUNNEL}:443`);
	// No address-free link is tried first: its token would be issued for a
	// listing AO Cloud refuses.
	expect(fake.log).toEqual([
		"/api/v1/mobile/enable",
		"/api/v1/mobile/remote-access",
		"/api/v1/remote-host/account-token",
		`cloud PUT ${fake.HOST_ID}`,
	]);
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

it("waits for the tunnel after linking", async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	fake.daemon.readsUntilTunnel = 3;
	renderAccess();
	fireEvent.click(await readySwitch());

	expect(await screen.findByText("Starting remote access…")).toBeVisible();
	expect(fake.cloud.hosts.get(fake.HOST_ID)?.url).toBe(`https://${fake.FIXED}:443`);

	await act(async () => {
		await vi.advanceTimersByTimeAsync(10_000);
	});

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(fake.daemon.tunnelHost).toBe(fake.FIXED);
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

it("offers to install cloudflared and unlinks again when there is no connector", async () => {
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

it("keeps this computer in the account when it was listed before a failed turn-on", async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	Object.assign(fake.daemon, { enabled: true, password: "pw-phone", noPublicTunnel: true, linked: true, readsUntilTunnel: Number.POSITIVE_INFINITY });
	fake.cloud.hosts.set(fake.HOST_ID, { label: "Dhruv’s Mac mini", url: `https://${fake.FIXED}:443`, token: fake.TOKEN });
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);
	await act(async () => {
		await vi.advanceTimersByTimeAsync(120_000);
	});

	expect(await screen.findByRole("alert")).toBeVisible();
	expect(fake.cloud.hosts.has(fake.HOST_ID)).toBe(true);
	expect(fake.log).not.toContain(`cloud DELETE ${fake.HOST_ID}`);
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

it("waits for the daemon's own cloudflared download, however long it takes", async () => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	// About two minutes of status reads: longer than the tunnel's own wait.
	Object.assign(fake.daemon, { connector: false, downloadReads: 60 });
	renderAccess();
	const toggle = await readySwitch();
	fireEvent.click(toggle);

	await act(async () => {
		await vi.advanceTimersByTimeAsync(150_000);
	});

	expect(await screen.findByText("Your devices see this computer as Dhruv’s Mac mini.")).toBeVisible();
	expect(toggle).toHaveAttribute("aria-checked", "true");
	expect(fake.daemon.tunnelHost).toBe(fake.FIXED);
});
