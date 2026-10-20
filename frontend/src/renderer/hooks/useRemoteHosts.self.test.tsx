import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => {
	const self = { hostId: "this-mac", label: "Macbook Air M1", url: "https://this-mac.test" };
	const other = { hostId: "other-mac", label: "Other Mac", url: "https://other-mac.test" };
	const saved = [self, other];
	const workspaceResponse = (local: boolean, path: string) => {
		const projectId = local ? "local-project" : "other-project";
		if (path === "/api/v1/projects") return { data: { projects: [{ id: projectId, name: local ? "agent-orchestrator" : "other-repo", path: "/fixture/repo" }] } };
		if (path === "/api/v1/sessions") return { data: { sessions: [{ id: local ? "local-session" : "other-session", projectId, displayName: "Continue work", harness: "codex", status: "working", statusReadiness: "ready", activity: { state: "idle", lastActivityAt: "2026-01-01T00:00:00Z" }, prs: [] }] } };
		throw new Error(`unexpected request ${path}`);
	};
	return { self, other, saved, cloudBaseUrl: "https://cloud.fixture.test",
		localGet: vi.fn(async (path: string) => workspaceResponse(true, path)),
		remoteGet: vi.fn(async (base: string, path: string) => workspaceResponse(base.endsWith("4000"), path)),
		remotes: {
			list: vi.fn(async () => saved.filter((host) => host.hostId !== self.hostId)),
			importAccountHost: vi.fn(async (_account: string, host: typeof self) => {
				const index = saved.findIndex((entry) => entry.hostId === host.hostId);
				if (index < 0) saved.push(host);
				else saved[index] = host;
			}),
			pruneAccountHosts: vi.fn(async () => undefined),
			connect: vi.fn(async (url: string, hostId: string) => ({ ...(hostId === self.hostId ? self : other), url, base: `http://127.0.0.1:${hostId === self.hostId ? "4000" : "4001"}` })),
			disconnect: vi.fn(async () => undefined),
		},
	};
});

vi.mock("../lib/bridge", () => ({ aoBridge: { remotes: mocks.remotes } }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: mocks.localGet }, hasTrustedApiBaseUrl: () => true }));
vi.mock("openapi-fetch", () => ({ default: ({ baseUrl }: { baseUrl: string }) => ({ GET: (path: string) => mocks.remoteGet(baseUrl, path) }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: "authenticated", session: { user: { id: "fixture-account" } } }) }));
vi.mock("./useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: mocks.cloudBaseUrl } }) }));
vi.mock("../lib/account-remote-hosts", () => ({ listAccountRemoteHosts: async () => [mocks.self, mocks.other].map((host) => ({ ...host, token: "fixture-only" })) }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ ready: false, client: {}, baseUrl: "" }) }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ ready: false, org: undefined }) }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../lib/agent-switch-visibility", () => ({ agentSwitchVisibility: { setQueryHealthy: vi.fn() } }));

import { useRemoteHosts } from "./useRemoteHosts";
import { useRemoteWorkspaces, useWorkspaceQuery } from "./useWorkspaceQuery";
import { connectedHosts, disconnectHost } from "../lib/host-clients";
import { useUiStore } from "../stores/ui-store";

beforeEach(() => {
	vi.clearAllMocks();
	mocks.remotes.list.mockReset().mockImplementation(async () => mocks.saved.filter((host) => host.hostId !== mocks.self.hostId));
	mocks.saved.splice(0, mocks.saved.length, mocks.self, mocks.other);
	mocks.cloudBaseUrl = "https://cloud.fixture.test";
	useUiStore.setState({ developerMode: true, remoteHosts: true });
});

afterEach(async () => {
	await act(async () => {
		useUiStore.setState({ developerMode: false, remoteHosts: false });
		await Promise.all(connectedHosts().map(disconnectHost));
	});
});

function renderWorkspaceSources() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	return renderHook(() => ({ hosts: useRemoteHosts(), local: useWorkspaceQuery(), remote: useRemoteWorkspaces() }), { wrapper });
}

it.each([true, false])("excludes this daemon from remote sidebar data while preserving another host (cloud sync: %s)", async (cloudSync) => {
	if (!cloudSync) mocks.cloudBaseUrl = "";
	else mocks.saved.splice(0);
	const { result } = renderWorkspaceSources();
	await waitFor(() => {
		expect(mocks.remotes.list).toHaveBeenCalled();
		if (cloudSync) expect(mocks.remotes.pruneAccountHosts).toHaveBeenCalled();
		expect(result.current.local.isSuccess).toBe(true);
		expect(result.current.hosts.hosts.every((host) => host.status === "connected")).toBe(true);
		expect(result.current.remote.loadedSessionHostIds).toHaveLength(result.current.hosts.hosts.length);
	});
	if (cloudSync) {
		expect(mocks.remotes.importAccountHost).toHaveBeenCalledWith("fixture-account", expect.objectContaining({ hostId: mocks.self.hostId }));
		expect(mocks.remotes.pruneAccountHosts).toHaveBeenCalledWith("fixture-account", [mocks.self.hostId, mocks.other.hostId]);
	}
	expect(mocks.saved.map((host) => host.hostId)).toEqual([mocks.self.hostId, mocks.other.hostId]);
	expect.soft(result.current.hosts.hosts.map((host) => host.hostId)).toEqual([mocks.other.hostId]);
	expect.soft(connectedHosts()).toEqual([mocks.other.hostId]);
	expect.soft(mocks.remotes.connect).not.toHaveBeenCalledWith(mocks.self.url, mocks.self.hostId);
	expect(mocks.remotes.connect).toHaveBeenCalledWith(mocks.other.url, mocks.other.hostId);
	expect.soft(mocks.remoteGet).not.toHaveBeenCalledWith("http://127.0.0.1:4000", expect.anything());
	const sidebarWorkspaces = [...(result.current.local.data ?? []), ...result.current.remote.data];
	expect.soft(sidebarWorkspaces.filter((workspace) => workspace.id === "local-project")).toHaveLength(1);
	expect.soft(sidebarWorkspaces.flatMap((workspace) => workspace.sessions).filter((session) => session.id === "local-session")).toHaveLength(1);
	expect(sidebarWorkspaces.find((workspace) => workspace.id === "other-project")?.hostId).toBe(mocks.other.hostId);
	await act(async () => result.current.hosts.refresh());
	expect(result.current.hosts.hosts.map((host) => host.hostId)).toEqual([mocks.other.hostId]);
	expect(mocks.remotes.connect).not.toHaveBeenCalledWith(mocks.self.url, mocks.self.hostId);
});

it("keeps a legacy host visible for re-pairing without a renderer identity probe", async () => {
	const legacy = { hostId: "", label: "Old Mac", url: "https://old-mac.test" };
	mocks.cloudBaseUrl = "";
	mocks.saved.splice(0, mocks.saved.length, legacy);
	mocks.remotes.connect.mockRejectedValueOnce(new Error("remote host must be paired again to record its identity"));
	const { result } = renderHook(() => useRemoteHosts());
	await waitFor(() => expect(result.current.hosts).toEqual([{ ...legacy, status: "offline" }]));
	expect(mocks.remotes.connect).toHaveBeenCalledWith(legacy.url, "");
	expect(mocks.saved).toEqual([legacy]);
});
