import { describe, expect, it, vi } from "vitest";
import { checkHostUpdate, createLocalHostIdentity, IncompatibleRemoteVersionError, pairDevice, probeRemote, readAuthErrorCode, readHostUpdate, readRemoteIdentity, startHostUpdate } from "./remote-request";

const entry = { label: "workbox", url: "http://192.0.2.1:3011", password: "pw" };

// Typed as fetch so `mock.calls` carries fetch's argument tuple — an untyped
// `vi.fn(async () => …)` records a zero-length tuple and indexing it is a type error.
function fakeFetch(status: number, body: unknown = {}) {
	return vi.fn<typeof fetch>(async () => new Response(JSON.stringify(body), { status }));
}

// A body that is not JSON at all, the way an SPA catch-all answers every path.
function fakeTextFetch(status: number, text: string) {
	return vi.fn<typeof fetch>(async () => new Response(text, { status }));
}

const daemonProbe = { status: "ok", service: "agent-orchestrator-daemon", pid: 1234 };

describe("createLocalHostIdentity", () => {
	it("uses only the confirmed loopback port and caches until the attachment changes", async () => {
		let attachment: { port: number; pid?: number; generation: number } | null = null;
		const identity = vi.fn().mockResolvedValueOnce("h_first").mockRejectedValueOnce(new Error("restarting")).mockResolvedValueOnce("h_second");
		const localIdentity = createLocalHostIdentity(() => attachment, identity);
		await expect(localIdentity()).resolves.toBeNull();
		expect(identity).not.toHaveBeenCalled();
		attachment = { port: 4001, generation: 1 };
		await expect(localIdentity()).resolves.toBe("h_first");
		await expect(localIdentity()).resolves.toBe("h_first");
		expect(identity).toHaveBeenCalledExactlyOnceWith({ url: "http://127.0.0.1:4001" });
		attachment = { port: 4001, generation: 2 };
		await expect(localIdentity()).resolves.toBeNull();
		await expect(localIdentity()).resolves.toBe("h_second");
		expect(identity).toHaveBeenCalledTimes(3);
	});

	it("ignores an identity response from an attachment that ended while the request was pending", async () => {
		let attachment: { port: number; pid: number; generation: number } | null = { port: 4001, pid: 1, generation: 1 };
		let finish!: (hostId: string) => void;
		const identity = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
		const localIdentity = createLocalHostIdentity(() => attachment, identity);
		const pending = localIdentity();
		attachment = { port: 4001, pid: 2, generation: 2 };
		finish("h_first");
		await expect(pending).resolves.toBeNull();
		const fresh = localIdentity();
		finish("h_second");
		await expect(fresh).resolves.toBe("h_second");
		attachment = null;
		await expect(localIdentity()).resolves.toBeNull();
	});
});

describe("readRemoteIdentity", () => {
	it("learns the host ID without sending a credential", async () => {
		const doFetch = fakeFetch(200, { hostId: "h_workbox", apiVersion: 1 });
		await expect(readRemoteIdentity(entry, doFetch)).resolves.toBe("h_workbox");
		const [url, init] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://192.0.2.1:3011/api/v1/identity");
		expect(new Headers(init.headers).has("Authorization")).toBe(false);
		expect(init.redirect).toBe("error");
	});
	it("rejects the desktop's reserved local host ID", async () => {
		await expect(readRemoteIdentity(entry, fakeFetch(200, { hostId: "local" }))).rejects.toThrow(/reserved/);
	});
	it.each([undefined, 2])("rejects incompatible API version %s before using the credential", async (apiVersion) => {
		await expect(readRemoteIdentity(entry, fakeFetch(200, { hostId: "h_workbox", apiVersion })))
			.rejects.toBeInstanceOf(IncompatibleRemoteVersionError);
	});
});

describe("host update", () => {
	it("reads the host's version with the credential and treats an older host as unknown", async () => {
		const doFetch = fakeFetch(200, { version: "0.14.10", supported: true, state: "failed", error: "Release SHA-256 mismatch." });
		await expect(readHostUpdate({ ...entry, hostId: "h_workbox" }, doFetch)).resolves.toEqual({ version: "0.14.10", supported: true, state: "failed", error: "Release SHA-256 mismatch." });
		const [url, init] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://192.0.2.1:3011/api/v1/remote-host/update");
		expect(new Headers(init.headers).get("X-AO-Expected-Host-ID")).toBe("h_workbox");
		expect(new Headers(init.headers).get("Authorization")).toMatch(/^Bearer /);
		await expect(readHostUpdate(entry, fakeFetch(404, { code: "NOT_FOUND" }))).resolves.toBeNull();
		await expect(readHostUpdate(entry, fakeFetch(200, { status: "ok" }))).resolves.toBeNull();
		await expect(readHostUpdate(entry, fakeFetch(200, { version: "0.16.0", supported: true, method: "app", state: "idle" }))).resolves.toEqual({ version: "0.16.0", supported: true, method: "app", state: "idle" });
		await expect(readHostUpdate(entry, fakeFetch(200, { version: "0.16.0", supported: true, method: "ssh", state: "idle" }))).resolves.toEqual({ version: "0.16.0", supported: true, state: "idle" });
	});
	it("starts an update and surfaces the host's refusal", async () => {
		await expect(startHostUpdate(entry, undefined, fakeFetch(202, { version: "0.14.10", supported: true, state: "updating" }))).resolves.toMatchObject({ state: "updating" });
		await expect(startHostUpdate(entry, undefined, fakeFetch(409, { code: "HOST_UPDATE_UNSUPPORTED", message: "update Orchestrator on that computer" }))).rejects.toThrow("update Orchestrator on that computer");
		await expect(startHostUpdate(entry, undefined, fakeFetch(500, { message: "could not start the update: no user bus" }))).rejects.toThrow("no user bus");
		await expect(startHostUpdate(entry, undefined, fakeFetch(530))).resolves.toBeNull();
		await expect(startHostUpdate(entry, undefined, vi.fn<typeof fetch>(async () => { throw new TypeError("fetch failed"); }))).resolves.toBeNull();
	});
	it("sends the confirmed version only when there is one", async () => {
		const doFetch = fakeFetch(202, { version: "0.15.0", supported: true, method: "app", state: "updating" });
		await startHostUpdate(entry, "0.16.0", doFetch);
		await startHostUpdate(entry, undefined, doFetch);
		const [, confirmed] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		const [, plain] = doFetch.mock.calls[1] as unknown as [string, RequestInit];
		expect(confirmed.body).toBe(JSON.stringify({ version: "0.16.0" }));
		expect(new Headers(confirmed.headers).get("Content-Type")).toBe("application/json");
		expect(plain.body).toBeUndefined();
	});
	it("asks the host which version it installs next", async () => {
		const doFetch = fakeFetch(200, { version: "0.16.0" });
		await expect(checkHostUpdate({ ...entry, hostId: "h_workbox" }, doFetch)).resolves.toBe("0.16.0");
		const [url, init] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://192.0.2.1:3011/api/v1/remote-host/update/check");
		expect(init.method).toBe("POST");
		expect(init.redirect).toBe("error");
		expect(new Headers(init.headers).get("X-AO-Expected-Host-ID")).toBe("h_workbox");
		await expect(checkHostUpdate(entry, fakeFetch(409, { code: "HOST_UPDATE_CHECK_FAILED", message: "No newer version is available for that computer." }))).rejects.toThrow("No newer version is available for that computer.");
		await expect(checkHostUpdate(entry, fakeFetch(404, { code: "NOT_FOUND" }))).rejects.toThrow("didn't say which version");
		await expect(checkHostUpdate(entry, vi.fn<typeof fetch>(async () => { throw new TypeError("fetch failed"); }))).rejects.toThrow("didn't say which version");
	});
});

describe("probeRemote", () => {
	it("reports online on a 200 that carries the daemon's own probe body", async () => {
		await expect(probeRemote(entry, fakeFetch(200, daemonProbe))).resolves.toBe("online");
	});

	it("sends the saved credential only to the configured host and never follows redirects", async () => {
		const doFetch = fakeFetch(200, daemonProbe);
		await probeRemote({ ...entry, hostId: "h_workbox", url: "http://192.0.2.1/ao/" }, doFetch);
		const [url, init] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://192.0.2.1/ao/healthz");
		expect(init.redirect).toBe("error");
		expect(new Headers(init.headers).get("Authorization")).toBe("Bearer pw");
		expect(new Headers(init.headers).get("X-AO-Expected-Host-ID")).toBe("h_workbox");
		await probeRemote({ ...entry, url: "workbox:3011" }, doFetch);
		expect(doFetch.mock.calls[1][0]).toBe("http://workbox:3011/healthz");
	});

	it("refuses embedded URL credentials before sending the saved credential", async () => {
		const doFetch = fakeFetch(200, daemonProbe);
		await expect(probeRemote({ ...entry, url: "http://attacker:secret@192.0.2.1:3011" }, doFetch)).resolves.toBe("offline");
		expect(doFetch).not.toHaveBeenCalled();
	});

	it("distinguishes a bad password from an unreachable host", async () => {
		await expect(probeRemote(entry, fakeFetch(401))).resolves.toBe("unauthorized");
		const refused = vi.fn(async () => {
			throw new TypeError("fetch failed");
		});
		await expect(probeRemote(entry, refused)).resolves.toBe("offline");
	});

	it("bounds probes to sleeping hosts", async () => {
		const sleeping = vi.fn<typeof fetch>((_input, init) =>
			new Promise((_resolve, reject) => {
				init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
			}),
		);

		await expect(probeRemote(entry, sleeping, 1)).resolves.toBe("offline");
		expect(sleeping.mock.calls[0]?.[1]?.signal).toBeInstanceOf(AbortSignal);
	});

	// The port typo that white-screened the app: :8081 was an Expo web server,
	// whose SPA catch-all answers /healthz with 200 and an HTML page. A status
	// code proves something replied, not that it speaks the daemon's protocol —
	// and once such a host became the api base, every query returned that page.
	it("rejects a 200 whose body is not a daemon probe", async () => {
		const html = fakeTextFetch(200, "<!DOCTYPE html><html><title>AO</title></html>");
		await expect(probeRemote(entry, html)).resolves.toBe("not-a-daemon");
	});

	it("rejects a 200 from a JSON service that is not the daemon", async () => {
		await expect(probeRemote(entry, fakeFetch(200, { status: "ok" }))).resolves.toBe("not-a-daemon");
		await expect(probeRemote(entry, fakeFetch(200, { ...daemonProbe, service: "grafana" }))).resolves.toBe(
			"not-a-daemon",
		);
	});
});

describe("pairDevice", () => {
	const host = { url: "https://vm.example", hostId: "h_vm" };
	const device = { name: "Test Mac", platform: "darwin" };
	const token = "aod_" + "a".repeat(64);

	it("sends the secret as the bearer with the expected host, and returns the device token", async () => {
		const fetchImpl = fakeFetch(200, { hostId: "h_vm", deviceId: "dev_1", token, credential: "pairing-code" });
		await expect(pairDevice(host, "Code1234", device, fetchImpl)).resolves.toEqual({ kind: "paired", token, credential: "pairing-code" });
		const [url, init] = fetchImpl.mock.calls[0];
		expect(url).toBe("https://vm.example/api/v1/remote-host/devices");
		expect(init?.method).toBe("POST");
		expect(init?.redirect).toBe("error");
		expect(new Headers(init?.headers).get("authorization")).toBe("Bearer Code1234");
		expect(new Headers(init?.headers).get("x-ao-expected-host-id")).toBe("h_vm");
		expect(JSON.parse(String(init?.body))).toEqual(device);
	});

	it.each([
		[401, "unauthorized"],
		[404, "unsupported"], [405, "unsupported"], [403, "unsupported"], [409, "unsupported"],
		[429, "offline"], [500, "offline"],
	])("maps %i to %s", async (status, kind) => {
		await expect(pairDevice(host, "secret", device, fakeFetch(status))).resolves.toEqual({ kind });
	});

	it("refuses an answer from another host or without a device token", async () => {
		await expect(pairDevice(host, "secret", device, fakeFetch(200, { hostId: "h_other", token }))).resolves.toEqual({ kind: "offline" });
		await expect(pairDevice(host, "secret", device, fakeFetch(200, { hostId: "h_vm", token: "b".repeat(64) }))).resolves.toEqual({ kind: "offline" });
		await expect(pairDevice(host, "secret", device, vi.fn<typeof fetch>(async () => { throw new TypeError("fetch failed"); }))).resolves.toEqual({ kind: "offline" });
	});
});

describe("readAuthErrorCode", () => {
	it("returns the code of a 401 and nothing otherwise", async () => {
		await expect(readAuthErrorCode(entry, fakeFetch(401, { code: "DEVICE_REVOKED" }))).resolves.toBe("DEVICE_REVOKED");
		await expect(readAuthErrorCode(entry, fakeFetch(200, { code: "DEVICE_REVOKED" }))).resolves.toBeUndefined();
		await expect(readAuthErrorCode(entry, fakeTextFetch(401, "<html>"))).resolves.toBeUndefined();
	});
});
