import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceSummary } from "../../types/workspace";
import { SessionAccessTag } from "./SessionAccessTag";
import { SharedWithYouNotifier, shareArrivals } from "./SharedWithYouNotifier";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	live: undefined as undefined | { you: string; viewers: Array<{ userId: string; role: string }> },
	liveCalls: 0,
}));
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

const storefront = workspace("p-store", "storefront", { sessions: { "s-fix": { byName: "Asha", grantId: "g1" } } }, [{ id: "orch", kind: "orchestrator" }, { id: "s-fix", title: "Fix checkout" }]);

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
			wholeProject: { byName: "Asha", viaTeamName: "Platform", grantId: "g2" },
			sessions: { "s-fix": { byName: "Asha", grantId: "g1" } },
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
		const many = Array.from({ length: 5 }, (_unused, index) => workspace(`p${index}`, `project ${index}`, { sessions: { [`s${index}`]: { grantId: `g${index}` } } }, [{ id: `s${index}`, title: `Session ${index}` }]));
		await act(async () => {
			await queryClient.fetchQuery({ queryKey: KEY, queryFn: async () => many });
		});
		const toasts = useUiStore.getState().globalToasts;
		expect(toasts).toHaveLength(3);
		expect(toasts[0]!.title).toBe("Someone shared Session 0 with you");
		// All five are remembered, so the two not shown don't pop up later.
		expect(JSON.parse(window.localStorage.getItem(STORAGE) ?? "[]")).toHaveLength(5);
	});
});

describe("SessionAccessTag", () => {
	beforeEach(() => {
		mocks.liveCalls = 0;
		mocks.live = undefined;
	});

	it("does nothing, and asks for nothing, on the person's own sessions", () => {
		const { container } = render(<SessionAccessTag session={{ id: "s1", cloud: { orgId: "org" } as never }} />);
		expect(container.textContent).toBe("");
		expect(mocks.liveCalls).toBe(0);
	});

	it("shows the live role and the team it came through", () => {
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "viewer" }] };
		const { container, rerender } = render(<SessionAccessTag session={{ id: "s1", cloud: { orgId: "org", shared: true, sharedVia: "Platform" } as never }} />);
		expect(container.textContent).toBe("Watch · via Platform");
		mocks.live = { you: "me", viewers: [{ userId: "me", role: "editor" }] };
		rerender(<SessionAccessTag session={{ id: "s1", cloud: { orgId: "org", shared: true } as never }} />);
		expect(container.textContent).toBe("Write to the agent");
	});
});
