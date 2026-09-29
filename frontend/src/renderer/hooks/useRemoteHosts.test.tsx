import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";

const remotes = vi.hoisted(() => ({
	list: vi.fn(),
	connect: vi.fn(),
	disconnect: vi.fn(),
	connected: vi.fn(),
}));

vi.mock("../lib/bridge", () => ({ aoBridge: { remotes } }));

import { connectedHosts } from "../lib/host-clients";
import { useRemoteHosts } from "./useRemoteHosts";

beforeEach(() => {
	remotes.list.mockReset().mockResolvedValue([
		{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" },
		{ hostId: "box-b", label: "Box B", url: "http://box-b:3001" },
	]);
	remotes.connect.mockReset().mockImplementation(async (url: string) => ({
		hostId: url.includes("box-a") ? "box-a" : "box-b",
		label: url.includes("box-a") ? "Box A" : "Box B",
		url,
		base: "http://127.0.0.1:4000",
	}));
	remotes.disconnect.mockReset().mockResolvedValue(undefined);
	useUiStore.setState({ remoteHosts: false });
});

afterEach(() => useUiStore.setState({ remoteHosts: false }));

it("does not connect to saved boxes until Remote hosts is enabled", async () => {
	const { result } = renderHook(() => useRemoteHosts());
	await waitFor(() => expect(result.current.hosts).toHaveLength(0));
	expect(remotes.list).not.toHaveBeenCalled();
	expect(remotes.connect).not.toHaveBeenCalled();
});

it("connects both saved boxes and exposes their stable IDs", async () => {
	useUiStore.setState({ remoteHosts: true });
	const { result } = renderHook(() => useRemoteHosts());
	await waitFor(() => expect(result.current.hosts).toEqual([
		{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" },
		{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" },
	]));
});

it("disconnects the prior proxy when a connected host becomes unreachable", async () => {
	remotes.list.mockResolvedValue([{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" }]);
	useUiStore.setState({ remoteHosts: true });
	const { result } = renderHook(() => useRemoteHosts());
	await waitFor(() => expect(result.current.hosts[0]?.status).toBe("connected"));
	remotes.connect.mockRejectedValueOnce(new Error("host offline"));
	await act(async () => result.current.refresh());
	expect(result.current.hosts[0]?.status).toBe("offline");
	expect(remotes.disconnect).toHaveBeenCalledWith("http://box-a:3001");
	expect(connectedHosts()).not.toContain("box-a");
});

it("does not disconnect a newer successful connection after an older refresh fails", async () => {
	remotes.list.mockResolvedValue([{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" }]);
	useUiStore.setState({ remoteHosts: true });
	const { result } = renderHook(() => useRemoteHosts());
	await waitFor(() => expect(result.current.hosts[0]?.status).toBe("connected"));
	let failOld!: (reason: Error) => void;
	remotes.connect.mockImplementationOnce(() => new Promise((_, reject) => { failOld = reject; }));
	const oldRefresh = result.current.refresh();
	await waitFor(() => expect(remotes.connect).toHaveBeenCalledTimes(2));
	const newRefresh = result.current.refresh();
	await act(async () => { await newRefresh; failOld(new Error("stale failure")); await oldRefresh; });
	expect(result.current.hosts[0]?.status).toBe("connected");
	expect(connectedHosts()).toContain("box-a");
	expect(remotes.disconnect).not.toHaveBeenCalledWith("http://box-a:3001");
});

it("closes a connection that finishes after the feature is disabled", async () => {
	let finishConnect!: (value: { hostId: string; label: string; url: string; base: string }) => void;
	remotes.list.mockResolvedValue([{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" }]);
	remotes.connect.mockImplementation(() => new Promise((resolve) => { finishConnect = resolve; }));
	useUiStore.setState({ remoteHosts: true });
	const { result } = renderHook(() => useRemoteHosts());
	await waitFor(() => expect(remotes.connect).toHaveBeenCalledOnce());
	act(() => useUiStore.setState({ remoteHosts: false }));
	await act(async () => finishConnect({ hostId: "box-a", label: "Box A", url: "http://box-a:3001", base: "http://127.0.0.1:4000" }));
	await waitFor(() => expect(remotes.disconnect).toHaveBeenCalledWith("http://box-a:3001"));
	expect(result.current.hosts).toEqual([]);
});
