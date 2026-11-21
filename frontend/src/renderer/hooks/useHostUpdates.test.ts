import { beforeEach, describe, expect, it, vi } from "vitest";
import type { HostUpdateView } from "../../main/remote-request";
import { aoBridge } from "../lib/bridge";
import { autoUpdateHosts, compareVersions, hostUpdateAction, refreshHostUpdate, startHostUpdate, useHostUpdateStore } from "./useHostUpdates";

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
		useHostUpdateStore.setState({ byUrl: {}, hostIds: {} });
		localStorage.clear();
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
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "failed" });
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

	it("keeps a refused start's reason through later polls", async () => {
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockRejectedValue(new Error("could not start the update: no user bus"));
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.14.10"));
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		await startHostUpdate(URL);
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "failed", error: "could not start the update: no user bus" });
	});

	it("ignores a read that started before the update", async () => {
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		let answer!: (value: HostUpdateView) => void;
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockReturnValueOnce(new Promise((done) => { answer = done; }));
		const read = refreshHostUpdate(URL);
		await new Promise((done) => setTimeout(done, 2));
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockResolvedValue(view("0.14.10", "updating"));
		await startHostUpdate(URL);
		answer(view("0.14.10"));
		await read;
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "updating" });
	});

	it("ignores an idle read issued while the start was still pending", async () => {
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		let accept!: (value: HostUpdateView) => void;
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockReturnValue(new Promise((done) => { accept = done; }));
		let answer!: (value: HostUpdateView) => void;
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockReturnValueOnce(new Promise((done) => { answer = done; }));
		const started = startHostUpdate(URL);
		const read = refreshHostUpdate(URL);
		accept(view("0.14.10", "updating"));
		await started;
		answer(view("0.14.10"));
		await read;
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "updating" });
	});

	it("keeps following a host that reports its update running past ten minutes", async () => {
		const refreshed = vi.fn();
		window.addEventListener("ao:remote-hosts-changed", refreshed);
		const read = vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.14.10", "updating"));
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10", "updating"), updating: { from: "0.14.10", since: Date.now() - 11 * 60_000 } });
		await refreshHostUpdate(URL);
		read.mockResolvedValue(null);
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "updating" });
		read.mockResolvedValue(view("0.15.0"));
		await refreshHostUpdate(URL);
		expect(refreshed).toHaveBeenCalledOnce();
		window.removeEventListener("ao:remote-hosts-changed", refreshed);
	});

	it("keeps following an update whose start got no answer while the host restarts", async () => {
		const refreshed = vi.fn();
		window.addEventListener("ao:remote-hosts-changed", refreshed);
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockResolvedValue(null);
		const read = vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(null);
		await startHostUpdate(URL);
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "updating" });
		read.mockResolvedValue(view("0.15.0"));
		await refreshHostUpdate(URL);
		expect(refreshed).toHaveBeenCalledOnce();
		window.removeEventListener("ao:remote-hosts-changed", refreshed);
	});

	it("adopts a finished update when the start response was lost", async () => {
		const refreshed = vi.fn();
		window.addEventListener("ao:remote-hosts-changed", refreshed);
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10") });
		vi.spyOn(aoBridge.remotes, "startHostUpdate").mockRejectedValue(new Error("request timed out"));
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.15.0"));
		await startHostUpdate(URL);
		expect(useHostUpdateStore.getState().byUrl[URL]).toMatchObject({ view: { version: "0.15.0" }, updating: undefined, error: undefined });
		expect(refreshed).toHaveBeenCalledOnce();
		window.removeEventListener("ao:remote-hosts-changed", refreshed);
	});

	it("shows the host's reason after a timed-out wait", async () => {
		const hostUpdate = vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(null);
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10", "updating"), updating: { from: "0.14.10", since: Date.now() - 11 * 60_000 } });
		await refreshHostUpdate(URL);
		hostUpdate.mockResolvedValue({ ...view("0.14.10", "failed"), error: "No space left on device" });
		await refreshHostUpdate(URL);
		expect(hostUpdateAction(useHostUpdateStore.getState().byUrl[URL], "0.15.0")).toEqual({ kind: "failed", error: "No space left on device" });
		hostUpdate.mockResolvedValue(view("0.14.10"));
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

	it("auto-updates each behind host once per version of this computer", async () => {
		const start = vi.spyOn(aoBridge.remotes, "startHostUpdate").mockResolvedValue(view("0.14.10", "updating"));
		const behind = "https://auto-behind.example";
		useHostUpdateStore.setState({ localVersion: "0.15.0", byUrl: {
			[behind]: { view: view("0.14.10") },
			"https://auto-failed.example": { view: { ...view("0.14.10", "failed"), error: "disk full" } },
			"https://auto-manual.example": { view: view("0.14.10", "idle", false) },
			"https://auto-current.example": { view: view("0.15.1") },
		} });
		autoUpdateHosts();
		expect(start).toHaveBeenCalledExactlyOnceWith(behind);
		expect(JSON.parse(localStorage.getItem("ao.autoUpdateRemoteHostAttempts") ?? "{}")).toEqual({ [behind]: "0.15.0" });
		await Promise.resolve();
		useHostUpdateStore.getState().patch(behind, { view: view("0.14.10"), updating: undefined, error: undefined });
		autoUpdateHosts();
		expect(start).toHaveBeenCalledOnce();
		useHostUpdateStore.setState({ localVersion: "0.15.1" });
		autoUpdateHosts();
		expect(start).toHaveBeenCalledTimes(2);
	});

	it("remembers an automatic attempt by host ID, not address", () => {
		const start = vi.spyOn(aoBridge.remotes, "startHostUpdate").mockRejectedValue(new Error("no user bus"));
		vi.spyOn(aoBridge.remotes, "hostUpdate").mockResolvedValue(view("0.14.10"));
		const oldUrl = "https://old-tunnel.example";
		const newUrl = "https://new-tunnel.example";
		useHostUpdateStore.setState({ localVersion: "0.15.0", hostIds: { [oldUrl]: "host-a" }, byUrl: { [oldUrl]: { view: view("0.14.10") } } });
		autoUpdateHosts();
		expect(start).toHaveBeenCalledExactlyOnceWith(oldUrl);
		useHostUpdateStore.setState({ hostIds: { [newUrl]: "host-a" }, byUrl: { [newUrl]: { view: view("0.14.10") } } });
		autoUpdateHosts();
		expect(start).toHaveBeenCalledOnce();
		useHostUpdateStore.setState({ hostIds: { [oldUrl]: "host-b" }, byUrl: { [oldUrl]: { view: view("0.14.10") } } });
		autoUpdateHosts();
		expect(start).toHaveBeenLastCalledWith(oldUrl);
		expect(start).toHaveBeenCalledTimes(2);
	});

	it("forgets hosts that went offline unless they are mid-update", () => {
		const other = "https://studio.example";
		useHostUpdateStore.getState().patch(URL, { view: view("0.14.10"), updating: { from: "0.14.10", since: Date.now() } });
		useHostUpdateStore.getState().patch(other, { view: view("0.14.10") });
		useHostUpdateStore.getState().retain([]);
		expect(Object.keys(useHostUpdateStore.getState().byUrl)).toEqual([URL]);
	});
});
