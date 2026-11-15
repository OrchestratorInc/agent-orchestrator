import { beforeEach, describe, expect, it, vi } from "vitest";
import type { HostUpdateView } from "../../main/remote-request";
import { aoBridge } from "../lib/bridge";
import { compareVersions, hostUpdateAction, refreshHostUpdate, startHostUpdate, useHostUpdateStore } from "./useHostUpdates";

const URL = "https://build-vm.example";
const view = (version: string, state: HostUpdateView["state"] = "idle", supported = true): HostUpdateView => ({ version, supported, state });

describe("compareVersions", () => {
	it("orders published builds within one channel only", () => {
		expect(compareVersions("0.14.10", "0.15.0")).toBeLessThan(0);
		expect(compareVersions("v0.15.0", "0.15.0")).toBe(0);
		expect(compareVersions("0.15.1-nightly.202611140625", "0.15.1-nightly.202611150625")).toBeLessThan(0);
		expect(compareVersions("0.15.2-nightly.202611010000", "0.15.1-nightly.202611150625")).toBeGreaterThan(0);
		expect(compareVersions("0.14.10", "0.15.1-nightly.202611150625")).toBeNull();
		expect(compareVersions("", "0.15.0")).toBeNull();
		expect(compareVersions("0.15.0-dev", "0.15.0")).toBeNull();
	});
});

describe("hostUpdateAction", () => {
	it("asks for an update only when the host is behind this computer", () => {
		expect(hostUpdateAction({ view: view("0.14.10") }, "0.15.0")).toEqual({ kind: "update", version: "0.14.10" });
		expect(hostUpdateAction({ view: view("0.15.0") }, "0.15.0")).toBeNull();
		expect(hostUpdateAction({ view: view("0.16.0") }, "0.15.0")).toBeNull();
		expect(hostUpdateAction({ view: view("0.14.10", "idle", false) }, "0.15.0")).toEqual({ kind: "manual", version: "0.14.10" });
		expect(hostUpdateAction({ view: { ...view("0.14.10", "failed"), error: "Release SHA-256 mismatch." } }, "0.15.0")).toEqual({ kind: "failed", error: "Release SHA-256 mismatch." });
		expect(hostUpdateAction({ view: view("0.15.0", "failed") }, "0.15.0")).toBeNull();
		expect(hostUpdateAction({ view: null, updating: { from: "0.14.10", since: 0 } }, "0.15.0")).toEqual({ kind: "updating" });
		expect(hostUpdateAction({ view: view("0.14.10") }, undefined)).toBeNull();
	});
});

describe("host update flow", () => {
	beforeEach(() => {
		useHostUpdateStore.setState({ byUrl: {}, urlByHost: {} });
		vi.restoreAllMocks();
	});

	it("follows an update through the host's restart to its new version", async () => {
		const read = vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.14.10"));
		const refreshed = vi.fn();
		window.addEventListener("ao:remote-hosts-changed", refreshed);
		await refreshHostUpdate(URL);
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockResolvedValue(view("0.14.10", "updating"));
		await startHostUpdate(URL);
		expect(useHostUpdateStore.getState().byUrl[URL]?.updating).toMatchObject({ from: "0.14.10" });

		read.mockResolvedValue(null);
		await refreshHostUpdate(URL);
		expect(useHostUpdateStore.getState().byUrl[URL]?.updating).toBeDefined();
		expect(refreshed).not.toHaveBeenCalled();

		read.mockResolvedValue(view("0.15.0"));
		await refreshHostUpdate(URL);
		expect(useHostUpdateStore.getState().byUrl[URL]).toMatchObject({ view: { version: "0.15.0" }, updating: undefined });
		expect(refreshed).toHaveBeenCalledOnce();
		window.removeEventListener("ao:remote-hosts-changed", refreshed);
	});

	it("stops waiting for a host that never answers again", async () => {
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(null);
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10", "updating"), updating: { from: "0.14.10", since: Date.now() - 11 * 60_000 } });
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toBeNull();
	});

	it("keeps the host's reason when its update fails", async () => {
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue({ ...view("0.14.10", "failed"), error: "Release SHA-256 mismatch." });
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockResolvedValue(view("0.14.10", "updating"));
		await startHostUpdate(URL);
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "failed", error: "Release SHA-256 mismatch." });
	});

	it("shows a refused start as a failure", async () => {
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockRejectedValue(new Error("an update is already running on this host"));
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		await startHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "failed", error: "an update is already running on this host" });
	});

	it("keeps following an update another client already started", async () => {
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockRejectedValue(new Error("an update is already running on this host"));
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.14.10", "updating"));
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		await startHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "updating" });
	});

	it("drops a stale failure once the host reports idle", async () => {
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10"), error: "host is offline" });
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.14.10"));
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "update", version: "0.14.10" });
	});

	it("keeps the known version through one failed read", async () => {
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(null);
		await refreshHostUpdate(URL);
		expect(useHostUpdateStore.getState().byUrl[URL]?.view?.version).toBe("0.14.10");
	});

	it("shows a retry as running even when an older failed read lands first", async () => {
		useHostUpdateStore.getState().patch(URL, { view: { ...view("0.14.10", "failed"), error: "disk full" } });
		let resolve!: (value: HostUpdateView) => void;
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockReturnValue(new Promise((done) => { resolve = done; }));
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue({ ...view("0.14.10", "failed"), error: "disk full" });
		const started = startHostUpdate(URL);
		await refreshHostUpdate(URL);
		resolve(view("0.14.10", "updating"));
		await started;
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "updating" });
	});

	it("forgets hosts that went offline unless they are mid-update", () => {
		const other = "https://studio.example";
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10"), updating: { from: "0.14.10", since: Date.now() } });
		useHostUpdateStore.getState().patch(other, { view: view("0.14.10") });
		useHostUpdateStore.getState().retain([]);
		expect(Object.keys(useHostUpdateStore.getState().byUrl)).toEqual([URL]);
	});
});
