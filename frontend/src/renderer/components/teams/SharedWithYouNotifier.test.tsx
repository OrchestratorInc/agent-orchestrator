import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceSummary } from "../../types/workspace";
import { SessionAccessTag } from "./SessionAccessTag";
import { SharedWithYouNotifier, shareArrivals } from "./SharedWithYouNotifier";
import { sharedSessionVia, workspaceShareOf } from "../../hooks/useWorkspaceQuery";
import type { CloudCpSharedProject } from "../../lib/cloud-cp";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	live: undefined as undefined | { you: string; viewers: Array<{ userId: string; role: string }> },
	liveCalls: 0,
	teams: true,
}));
vi.mock("../../hooks/useTeamsEnabled", () => ({ useTeamsEnabled: () => mocks.teams }));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: true, baseUrl: "https://cp.test", userId: "me" }) }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => mocks.navigate }));
vi.mock("../../hooks/useCloudSessionLive", () => ({
	useCloudSessionLive: () => {
		mocks.liveCalls += 1;
		return mocks.live;
	},
}));

const KEY = ["cloud-shared-workspaces", "https://cp.test", "me"];
const STORAGE = "ao.cloud.seenShares.v1:https://cp.test:me";

function workspace(id: string, name: string, shared: WorkspaceSummary["shared"], sessions: Array<{ id: string; title?: string; kind?: string }> = []): WorkspaceSummary {
	return { id, name, shared, sessions } as unknown as WorkspaceSummary;
}

const storefront = workspace("p-store", "storefront", { sessions: { "s-fix": { byName: "Asha", grantId: "g1", source: "direct" } } }, [{ id: "orch", kind: "orchestrator" }, { id: "s-fix", title: "Fix checkout" }]);

function mount(queryClient: QueryClient) {
	return render(<QueryClientProvider client={queryClient}><SharedWithYouNotifier /></QueryClientProvider>);
}

describe("shareArrivals", () => {
	it("keys each share by its grant, so re-sharing after a stop is new", () => {
		const arrivals = shareArrivals([
			workspace("p1", "storefront", { wholeProject: { grantId: "gp" }, sessions: { s1: { grantId: "gs" } } }),
			workspace("p2", "local", undefined),
		]);
		expect(arrivals.map((arrival) => arrival.key)).toEqual(["p1:project:gp", "p1:s1:gs"]);
	});
});

describe("SharedWithYouNotifier", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ globalToasts: [] });
		mocks.navigate.mockReset();
		mocks.teams = true;
	});

	it("in a build without Teams announces nothing and keeps no record", async () => {
		mocks.teams = false;
		window.localStorage.setItem(STORAGE, JSON.stringify([]));
		const queryClient = new QueryClient();
		mount(queryClient);
		await act(async () => {
			await queryClient.fetchQuery({ queryKey: KEY, queryFn: async () => [storefront] });
		});
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
		expect(JSON.parse(window.localStorage.getItem(STORAGE) ?? "null")).toEqual([]);
	});

	it("remembers what's already shared silently on the first load", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(KEY, [storefront]);
		mount(queryClient);
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
		expect(JSON.parse(window.localStorage.getItem(STORAGE) ?? "[]")).toEqual(["p-store:s-fix:g1"]);
	});

	it("announces a new share once, through the team, and opens it", async () => {
		window.localStorage.setItem(STORAGE, JSON.stringify(["p-store:s-fix:g1"]));
		const queryClient = new QueryClient();
		mount(queryClient);
		const next = workspace("p-store", "storefront", {
			wholeProject: { byName: "Asha", viaTeamName: "Platform", grantId: "g2", source: "team" },
			sessions: { "s-fix": { byName: "Asha", grantId: "g1", source: "direct" } },
		}, [{ id: "orch", kind: "orchestrator" }, { id: "s-fix", title: "Fix checkout" }]);
		await act(async () => {
			await queryClient.fetchQuery({ queryKey: KEY, queryFn: async () => [next] });
		});
		const toasts = useUiStore.getState().globalToasts;
		expect(toasts.map((toast) => toast.title)).toEqual(["Asha shared storefront with Platform"]);
		toasts[0]!.action!.run();
		expect(mocks.navigate).toHaveBeenCalledWith("p-store", "orch");
		// The same list again says nothing.
		await act(async () => {
			await queryClient.fetchQuery({ queryKey: KEY, queryFn: async () => [next], staleTime: 0 });
		});
		expect(useUiStore.getState().globalToasts).toHaveLength(1);
	});

	it("names the session for a session share and caps a burst at three", async () => {
		window.localStorage.setItem(STORAGE, JSON.stringify([]));
		const queryClient = new QueryClient();
		mount(queryClient);
		const many = Array.from({ length: 5 }, (_unused, index) => workspace(`p${index}`, `project ${index}`, { sessions: { [`s${index}`]: { grantId: `g${index}`, source: "direct" } } }, [{ id: `s${index}`, title: `Session ${index}` }]));
		await act(async () => {
			await queryClient.fetchQuery({ queryKey: KEY, queryFn: async () => many });
		});
		const toasts = useUiStore.getState().globalToasts;
		expect(toasts).toHaveLength(3);
		expect(toasts[0]!.title).toBe("Someone shared Session 0 with you");
		// All five are remembered, so the two not shown don't pop up later.
		expect(JSON.parse(window.localStorage.getItem(STORAGE) ?? "[]")).toHaveLength(5);
	});

	it("doesn't announce a link the person opened themselves, but remembers it", async () => {
		window.localStorage.setItem(STORAGE, JSON.stringify([]));
		const queryClient = new QueryClient();
		mount(queryClient);
		const redeemed = workspace("p-store", "storefront", { sessions: { "s-fix": { byName: "Asha", grantId: "g-link", source: "link" } } }, [{ id: "s-fix", title: "Fix checkout" }]);
		// A control plane from before teams says nothing about how; it had only links.
		const older = workspace("p-old", "legacy", { sessions: { "s-old": { byName: "Asha", grantId: "g-old" } } }, [{ id: "s-old", title: "Old" }]);
		await act(async () => {
			await queryClient.fetchQuery({ queryKey: KEY, queryFn: async () => [redeemed, older] });
		});
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
		expect(JSON.parse(window.localStorage.getItem(STORAGE) ?? "[]")).toEqual(["p-store:s-fix:g-link", "p-old:s-old:g-old"]);
	});
});

describe("workspaceShareOf", () => {
	const item = (extra: Partial<CloudCpSharedProject>): CloudCpSharedProject => ({
		grant: { id: "g", role: "viewer", status: "active", deniedCommands: [], redeemedAt: "2026-10-30T00:00:00Z" },
		project: { id: "p", orgId: "o" } as CloudCpSharedProject["project"],
		...extra,
	});

	it("keeps how each share reached the person", () => {
		const share = workspaceShareOf([
			item({ grant: { ...item({}).grant, id: "g-whole" }, source: "team", viaTeamName: "Platform" }),
			item({ grant: { ...item({}).grant, id: "g-link" }, sessionId: "s1", source: "link" }),
		]);
		expect(share.wholeProject).toEqual({ grantId: "g-whole", viaTeamName: "Platform", source: "team" });
		expect(share.sessions?.s1).toEqual({ grantId: "g-link", source: "link" });
	});

	it("names the whole project's team only for a session not shared on its own", () => {
		const share = workspaceShareOf([
			item({ grant: { ...item({}).grant, id: "g-whole" }, source: "team", viaTeamName: "Platform" }),
			item({ grant: { ...item({}).grant, id: "g-direct" }, sessionId: "s-direct", source: "direct" }),
			item({ grant: { ...item({}).grant, id: "g-team" }, sessionId: "s-team", source: "team", viaTeamName: "Data infra" }),
		]);
		expect(sharedSessionVia(share, "s-direct")).toBeUndefined();
		expect(sharedSessionVia(share, "s-team")).toBe("Data infra");
		expect(sharedSessionVia(share, "s-other")).toBe("Platform");
	});
});

describe("SessionAccessTag", () => {
	const SHARED_LIST = ["cloud-shared-workspaces", "https://cp.test"];
	let queryClient: QueryClient;
	const tag = (session: Parameters<typeof SessionAccessTag>[0]["session"]) => (
		<QueryClientProvider client={queryClient}><SessionAccessTag session={session} /></QueryClientProvider>
	);
	beforeEach(() => {
		mocks.liveCalls = 0;
		mocks.live = undefined;
		mocks.teams = true;
		queryClient = new QueryClient();
		queryClient.setQueryData(SHARED_LIST, []);
	});

	it("in a build without Teams shows no tag, and asks for nothing", () => {
		mocks.teams = false;
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "viewer" }] };
		const { container } = render(tag({ id: "s1", cloud: { orgId: "org", shared: true, sharedVia: "Platform" } as never }));
		expect(container.textContent).toBe("");
		expect(mocks.liveCalls).toBe(0);
	});

	it("does nothing, and asks for nothing, on the person's own sessions", () => {
		const { container } = render(tag({ id: "s1", cloud: { orgId: "org" } as never }));
		expect(container.textContent).toBe("");
		expect(mocks.liveCalls).toBe(0);
	});

	it("asks for nothing on a session that has ended", () => {
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "viewer" }] };
		const { container } = render(tag({ id: "s1", isTerminated: true, cloud: { orgId: "org", shared: true } as never }));
		expect(container.textContent).toBe("");
		expect(mocks.liveCalls).toBe(0);
	});

	it("shows the live role and the team it came through", () => {
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "viewer" }] };
		const { container, rerender } = render(tag({ id: "s1", cloud: { orgId: "org", shared: true, sharedVia: "Platform" } as never }));
		expect(container.textContent).toBe("Watch · via Platform");
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "editor" }] };
		rerender(tag({ id: "s1", cloud: { orgId: "org", shared: true } as never }));
		expect(container.textContent).toBe("Write to the agent");
	});

	it("names no team while the shared list catches up with a role change, and asks it at once", async () => {
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "viewer" }] };
		const session = { id: "s1", cloud: { orgId: "org", shared: true, sharedVia: "Recipient QA" } as never };
		const { container, rerender } = render(tag(session));
		expect(container.textContent).toBe("Watch · via Recipient QA");
		// A direct share raises the role above the team's; the list still says
		// the team until it is read again.
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "editor" }] };
		rerender(tag(session));
		expect(container.textContent).toBe("Write to the agent");
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-shared-workspaces"] });
		// Once the list answers after the change, its team shows again.
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 5));
			queryClient.setQueryData(SHARED_LIST, []);
		});
		expect(container.textContent).toBe("Write to the agent · via Recipient QA");
	});
});
