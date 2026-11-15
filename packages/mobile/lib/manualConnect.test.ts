import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import {
	AccountManagedHostError,
	adoptManualConnection,
	DifferentMachineError,
	editedManualHost,
	persistManualHostEdit,
} from "./manualConnect";

// A literal rather than DEFAULT_CONFIG: config.ts reaches native storage at
// import time, and this is pure logic that needs none of it.
const cfg: ServerConfig = {
	host: "192.168.1.42",
	httpPort: "3011",
	muxPort: "14801",
	secure: false,
	password: "pw",
};

function deps(over: Partial<Parameters<typeof adoptManualConnection>[1]> = {}) {
	return {
		identity: vi.fn(async () => "h_manual"),
		saveHost: vi.fn(async () => {}),
		setActiveHost: vi.fn(async () => {}),
		...over,
	};
}

// The bug: ManualConnectSheet wrote only the legacy ServerConfig. Once a host
// list exists, migration skips and resolution reconnects the active host — so a
// manual connection was silently replaced by the previous machine on reload.
describe("adopting a manual connection", () => {
	it("rejects a blank machine name before persisting", async () => {
		const d = deps();

		await expect(adoptManualConnection(cfg, d, "   ")).rejects.toThrow("Machine name is required");
		expect(d.saveHost).not.toHaveBeenCalled();
		expect(d.setActiveHost).not.toHaveBeenCalled();
	});

	it("stores the machine so it survives a reload", async () => {
		const d = deps();

		await adoptManualConnection(cfg, d, "Studio Mac");

		expect(d.saveHost).toHaveBeenCalledWith(
			expect.objectContaining({
				id: "h_manual",
				token: "pw",
				endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }],
			}),
		);
	});

	it("makes it the active machine, not merely another entry", async () => {
		const d = deps();
		await adoptManualConnection(cfg, d, "Studio Mac");
		expect(d.setActiveHost).toHaveBeenCalledWith("h_manual");
	});

	// A TLS address entered by hand is the tailnet path; plain is the LAN.
	it("records a secure address as a tailscale endpoint", async () => {
		const d = deps();
		await adoptManualConnection({ ...cfg, secure: true, httpPort: "443" }, d, "Studio Mac");
		expect(d.saveHost).toHaveBeenCalledWith(
			expect.objectContaining({
				endpoints: [{ kind: "tailscale", host: "192.168.1.42", port: 443, secure: true }],
			}),
		);
	});

	it("uses a chosen name and stores a hostname rather than a pasted URL", async () => {
		const d = deps();
		await adoptManualConnection({ ...cfg, host: "https://example.trycloudflare.com/", secure: true, httpPort: "443" }, d, " AzureLinux ");
		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({
			name: "AzureLinux",
			endpoints: [{ kind: "tailscale", host: "example.trycloudflare.com", port: 443, secure: true }],
		}));
	});

	// An older daemon has no identity probe. The machine is still worth storing
	// — it just stays unverified until it reports an id, exactly as a migrated
	// pairing does.
	it("stores the machine even when it reports no identity", async () => {
		const d = deps({ identity: vi.fn(async () => "") });

		const id = await adoptManualConnection(cfg, d, "Studio Mac");

		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({ id: "" }));
		expect(id).toBe("");
	});
});

it("edits one saved address and password without dropping other addresses or identity", () => {
	const host = {
		id: "h_manual", name: "Old", platform: "linux", token: "old", lastConnected: 42,
		endpoints: [
			{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false },
			{ kind: "tunnel" as const, host: "old.trycloudflare.com", port: 443, secure: true },
		],
	};
	const edited = editedManualHost(host, { ...cfg, host: "new.trycloudflare.com", httpPort: "443", secure: true, password: "new" }, " AzureLinux ", 1);
	expect(edited).toEqual({ ...host, name: "AzureLinux", token: "new", endpoints: [host.endpoints[0], { kind: "tunnel", host: "new.trycloudflare.com", port: 443, secure: true }] });
});

describe("persisting an inline machine edit", () => {
	const host = {
		id: "h_manual", name: "Old", platform: "linux", token: "old", lastConnected: 42,
		endpoints: [
			{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false },
			{ kind: "tunnel" as const, host: "old.trycloudflare.com", port: 443, secure: true },
		],
	};

	function editDeps(overrides: Partial<Parameters<typeof persistManualHostEdit>[4]> = {}) {
		return {
			probe: vi.fn(async () => ({ hostId: "h_manual" })),
			ping: vi.fn(async () => {}),
			saveHost: vi.fn(async () => {}),
			renameHost: vi.fn(async () => {}),
			...overrides,
		};
	}

	it("refuses route or password edits on an account-synced machine but still renames it", async () => {
		const managed = { ...host, accountUserId: "user-a" };
		for (const edit of [{ host: "10.0.0.9", password: "old" }, { host: "192.168.1.42", password: "new" }]) {
			const d = editDeps();
			await expect(persistManualHostEdit(managed, { ...cfg, ...edit }, "Studio Mac", 0, d)).rejects.toBeInstanceOf(AccountManagedHostError);
			expect(d.probe).not.toHaveBeenCalled();
			expect(d.ping).not.toHaveBeenCalled();
			expect(d.saveHost).not.toHaveBeenCalled();
		}
		const d = editDeps();
		await persistManualHostEdit(managed, { ...cfg, host: "192.168.1.42", password: "old" }, "Studio Mac", 0, d);
		expect(d.renameHost).toHaveBeenCalledWith("h_manual", "Studio Mac");
	});

	it("renames locally without probing an unchanged connection", async () => {
		const d = editDeps();

		await persistManualHostEdit(
			host,
			{ ...cfg, host: "192.168.1.42", password: "old" },
			" Studio Mac ",
			0,
			d,
		);

		expect(d.renameHost).toHaveBeenCalledWith("h_manual", "Studio Mac");
		expect(d.probe).not.toHaveBeenCalled();
		expect(d.ping).not.toHaveBeenCalled();
		expect(d.saveHost).not.toHaveBeenCalled();
	});

	it("verifies a changed route before authenticating and saving it", async () => {
		const order: string[] = [];
		const d = editDeps({
			probe: vi.fn(async () => { order.push("probe"); return { hostId: "h_manual" }; }),
			ping: vi.fn(async () => { order.push("ping"); }),
			saveHost: vi.fn(async () => { order.push("save"); }),
		});

		const saved = await persistManualHostEdit(
			host,
			{ ...cfg, host: " https://new.trycloudflare.com/ ", httpPort: "443", secure: true, password: "new" },
			"AzureLinux",
			1,
			d,
		);

		expect(order).toEqual(["probe", "ping", "save"]);
		expect(d.ping).toHaveBeenCalledWith(expect.objectContaining({
			host: "new.trycloudflare.com",
			httpPort: "443",
			password: "new",
			hostId: "h_manual",
		}));
		expect(saved.endpoints[1]).toEqual({ kind: "tunnel", host: "new.trycloudflare.com", port: 443, secure: true });
	});

	it("splits a pasted host:port instead of saving host:443 with another port on top (#7981)", async () => {
		const d = editDeps();

		const saved = await persistManualHostEdit(
			host,
			{ ...cfg, host: "https://new.trycloudflare.com:443", httpPort: "", secure: true, password: "old" },
			"Old",
			1,
			d,
		);

		expect(d.ping).toHaveBeenCalledWith(expect.objectContaining({ host: "new.trycloudflare.com", httpPort: "443" }));
		expect(saved.endpoints[1]).toEqual({ kind: "tunnel", host: "new.trycloudflare.com", port: 443, secure: true });
	});

	it("refuses a host and Port that name different ports before probing", async () => {
		const d = editDeps();

		await expect(persistManualHostEdit(
			host,
			{ ...cfg, host: "new.trycloudflare.com:443", httpPort: "3011", secure: true },
			"Old",
			1,
			d,
		)).rejects.toThrow("The host says port 443 but Port is 3011");

		expect(d.probe).not.toHaveBeenCalled();
	});

	it("does not send a credential when the changed address belongs to another machine", async () => {
		const d = editDeps({ probe: vi.fn(async () => ({ hostId: "someone-else" })) });

		await expect(persistManualHostEdit(
			host,
			{ ...cfg, host: "10.0.0.9", password: "new" },
			"Old",
			0,
			d,
		)).rejects.toBeInstanceOf(DifferentMachineError);

		expect(d.ping).not.toHaveBeenCalled();
		expect(d.saveHost).not.toHaveBeenCalled();
	});
});

describe("a newly typed code or password on a saved machine", () => {
	const host = { id: "h_manual", name: "Old", platform: "linux", token: "old", fallbackToken: "older", lastConnected: 42, endpoints: [{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false }] };
	const token = "aod_" + "a".repeat(64);
	const editDeps = (pair: NonNullable<Parameters<typeof persistManualHostEdit>[4]["pair"]>) => ({
		probe: vi.fn(async () => ({ hostId: "h_manual" })),
		ping: vi.fn(async () => {}),
		saveHost: vi.fn(async () => {}),
		renameHost: vi.fn(async () => {}),
		pair: vi.fn(pair),
	});

	it.each([
		["a one-time code", "pairing-code" as const, undefined],
		["the pairing password", "password" as const, "typed"],
	])("swaps %s for this phone's own token after the identity probe", async (_name, credential, fallbackToken) => {
		const d = editDeps(async () => ({ kind: "paired", token, credential }));
		const saved = await persistManualHostEdit(host, { ...cfg, password: "typed" }, "Old", 0, d);

		expect(d.probe.mock.invocationCallOrder[0]).toBeLessThan(d.pair.mock.invocationCallOrder[0]);
		expect(d.pair).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ password: "typed", hostId: "h_manual" }));
		expect(d.ping).toHaveBeenCalledWith(expect.objectContaining({ password: token }));
		expect(saved.token).toBe(token);
		expect(saved.fallbackToken).toBe(fallbackToken);
		expect(d.saveHost).toHaveBeenCalledWith(saved);
	});

	it("saves the typed password, and drops the old fallback, on a machine without device tokens", async () => {
		const d = editDeps(async () => ({ kind: "unsupported" }));
		const saved = await persistManualHostEdit(host, { ...cfg, password: "typed" }, "Old", 0, d);
		expect(d.ping).toHaveBeenCalledWith(expect.objectContaining({ password: "typed" }));
		expect(saved.token).toBe("typed");
		expect(saved.fallbackToken).toBeUndefined();
	});

	it("pairs nothing when only the address changes, and keeps the fallback", async () => {
		const d = editDeps(async () => ({ kind: "unsupported" }));
		const saved = await persistManualHostEdit(host, { ...cfg, host: "192.168.1.50", password: "old" }, "Old", 0, d);
		expect(d.pair).not.toHaveBeenCalled();
		expect(saved.fallbackToken).toBe("older");
	});
});
