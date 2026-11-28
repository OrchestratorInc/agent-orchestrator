import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PendingApproval } from "../../lib/approvals";

const state = vi.hoisted(() => ({
	pending: [] as PendingApproval[],
	remote: [] as PendingApproval[],
	cloud: [] as PendingApproval[],
	watch: vi.fn(),
	liveReads: 0,
	routedSessionId: undefined as string | undefined,
	routedHostId: undefined as string | undefined,
	decide: vi.fn(async () => undefined),
	navigate: vi.fn(),
}));

vi.mock("../../lib/approvals", () => ({
	usePendingApprovals: () => ({ data: state.pending }),
	useRemotePendingApprovals: () => state.remote,
	decideApproval: state.decide,
}));
vi.mock("../../lib/cloud-approvals", () => ({
	useCloudPendingApprovals: () => state.cloud,
	useLiveCloudPendingApprovals: () => {
		state.liveReads += 1;
		return state.cloud;
	},
	useWatchCloudApprovals: state.watch,
}));
vi.mock("@tanstack/react-router", () => ({
	useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
		select({
			matches: [
				{
					params: state.routedSessionId
						? { sessionId: state.routedSessionId, ...(state.routedHostId ? { hostId: state.routedHostId } : {}) }
						: {},
				},
			],
		}),
}));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => state.navigate }));
vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "proj", sessions: [{ id: "ao-2", title: "Book flights" }] }] }),
}));

import { useUiStore } from "../../stores/ui-store";
import { CloudApprovalReader, GlobalApprovalCard, SessionApprovalDock, SessionChatApprovalCard } from "./ApprovalHost";

function request(id: string, sessionId: string): PendingApproval {
	return {
		id, kind: "browser-action", sessionId, site: "shop.example", action: `Click "Pay" (${id})`, category: "payment",
		reason: "reads like a payment", createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 90_000).toISOString(),
	};
}

function renderWith(node: React.ReactNode) {
	return render(<QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider>);
}

describe("approval hosts", () => {
	beforeEach(() => {
		state.pending = [];
		state.remote = [];
		state.cloud = [];
		state.watch.mockClear();
		state.routedSessionId = undefined;
		state.routedHostId = undefined;
		state.decide.mockClear();
		state.navigate.mockClear();
	});

	it("shows a session's oldest request in that session only", async () => {
		state.pending = [request("a", "ao-1"), request("b", "ao-2"), request("c", "ao-1")];
		renderWith(<SessionApprovalDock sessionId="ao-1" />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(a)");
		expect(screen.getByText("+1 more")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
		await waitFor(() => expect(state.decide).toHaveBeenCalledWith("a", "allow-once", expect.anything(), undefined, undefined));
	});

	it("shows a session's request at the end of its chat and leaves once decided", async () => {
		state.pending = [request("a", "ao-1"), request("b", "ao-2")];
		const { container, rerender } = renderWith(<SessionChatApprovalCard sessionId="ao-1" />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(a)");
		expect(screen.getByText(/Payment/)).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
		await waitFor(() => expect(state.decide).toHaveBeenCalledWith("a", "allow-once", expect.anything(), undefined, undefined));
		state.pending = [request("b", "ao-2")];
		rerender(<QueryClientProvider client={new QueryClient()}><SessionChatApprovalCard sessionId="ao-1" /></QueryClientProvider>);
		expect(container).toBeEmptyDOMElement();
	});

	it("keeps Always allow behind Allow's menu, and Escape denies in the chat card", async () => {
		state.pending = [request("a", "ao-1")];
		renderWith(<SessionChatApprovalCard sessionId="ao-1" />);
		expect(screen.queryByText("Always allow on shop.example")).not.toBeInTheDocument();
		fireEvent.keyDown(screen.getByRole("button", { name: "Allow once" }), { key: "Enter" });
		expect(state.decide).not.toHaveBeenCalled();
		fireEvent.keyDown(screen.getByTestId("approval-prompt"), { key: "Escape" });
		await waitFor(() => expect(state.decide).toHaveBeenCalledWith("a", "deny", expect.anything(), undefined, undefined));
	});

	it("renders nothing for a session with no requests", () => {
		state.pending = [request("b", "ao-2")];
		const { container } = renderWith(<SessionApprovalDock sessionId="ao-1" />);
		expect(container).toBeEmptyDOMElement();
	});

	it("shows other sessions' requests app-wide, named, and opens the session", () => {
		state.pending = [request("a", "ao-1"), request("b", "ao-2")];
		state.routedSessionId = "ao-1";
		renderWith(<GlobalApprovalCard />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(b)");
		fireEvent.click(screen.getByRole("button", { name: "Book flights" }));
		expect(state.navigate).toHaveBeenCalledWith("proj", "ao-2", undefined);
	});

	it("shows a self-hosted remote host's request app-wide and decides it on that host", async () => {
		state.remote = [{ ...request("r", "ao-9"), hostId: "box-b" }];
		state.routedSessionId = "ao-1";
		renderWith(<GlobalApprovalCard />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(r)");
		fireEvent.click(screen.getByRole("button", { name: /Deny/ }));
		await waitFor(() => expect(state.decide).toHaveBeenCalledWith("r", "deny", expect.anything(), "box-b", undefined));
		fireEvent.click(screen.getByRole("button", { name: "ao-9" }));
		expect(state.navigate).toHaveBeenCalledWith(undefined, "ao-9", "box-b");
	});

	it("keeps a remote session's request in that session's dock, not the local one with the same id", () => {
		state.pending = [request("local", "ao-9")];
		state.remote = [{ ...request("r", "ao-9"), hostId: "box-b" }];
		state.routedSessionId = "ao-9";
		state.routedHostId = "box-b";
		renderWith(<SessionApprovalDock sessionId="ao-9" hostId="box-b" />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(r)");
		const { unmount } = renderWith(<GlobalApprovalCard />);
		// The open remote session shows its own; the local one is elsewhere.
		expect(screen.getAllByTestId("approval-action").map((node) => node.textContent)).toEqual(
			expect.arrayContaining([expect.stringContaining("(local)")]),
		);
		unmount();
	});

	it("shows the open session's request app-wide while Settings covers the session", () => {
		state.pending = [request("a", "ao-1")];
		state.routedSessionId = "ao-1";
		useUiStore.setState({ settingsModal: { scope: "global" } } as never);
		try {
			renderWith(<GlobalApprovalCard />);
			expect(screen.getByTestId("approval-action")).toHaveTextContent("(a)");
		} finally {
			useUiStore.setState({ settingsModal: null } as never);
		}
	});

	it("offers no Always allow for the catch-all script category on any card", () => {
		// The daemon refuses a site rule for "script" (eval, wait --fn, unknown
		// commands, unreadable targets), so no card may offer one.
		const script = { ...request("a", "ao-2"), category: "script", action: 'Run "wait --fn …"' } as PendingApproval;
		state.pending = [script];
		const app = renderWith(<GlobalApprovalCard />);
		expect(screen.queryByRole("checkbox", { name: /Always allow/ })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Allow once" })).toBeInTheDocument();
		app.unmount();
		state.pending = [{ ...script, sessionId: "ao-1" }];
		renderWith(<SessionChatApprovalCard sessionId="ao-1" />);
		expect(screen.queryByRole("button", { name: "More approval options" })).not.toBeInTheDocument();
		expect(screen.queryByText(/Always allow/)).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Allow once" })).toBeInTheDocument();
	});

	it("allows a site for good only when its box is ticked", async () => {
		state.pending = [request("a", "ao-2")];
		renderWith(<GlobalApprovalCard />);
		fireEvent.click(screen.getByRole("checkbox", { name: "Always allow on shop.example" }));
		fireEvent.click(screen.getByRole("button", { name: "Allow" }));
		await waitFor(() => expect(state.decide).toHaveBeenCalledWith("a", "allow-site", expect.anything(), undefined, undefined));
	});

	it("previews why the action was flagged", () => {
		state.pending = [request("a", "ao-2")];
		renderWith(<GlobalApprovalCard />);
		expect(screen.queryByText("reads like a payment")).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: "Preview" }));
		expect(screen.getByText("reads like a payment")).toBeVisible();
	});

	it("stays out of the way when the only request belongs to the open session", () => {
		state.pending = [request("a", "ao-1")];
		state.routedSessionId = "ao-1";
		const { container } = renderWith(<GlobalApprovalCard />);
		expect(container).toBeEmptyDOMElement();
	});

	it("shows a cloud session's request in its dock and decides it through the cloud", async () => {
		const cloudRequest = { ...request("c", "cloud-1"), cloud: { baseUrl: "https://cp.test", orgId: "org-1" } };
		state.cloud = [cloudRequest];
		renderWith(<SessionApprovalDock sessionId="cloud-1" cloudOrgId="org-1" />);
		expect(state.watch).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(c)");
		fireEvent.click(screen.getByRole("button", { name: /Deny/ }));
		await waitFor(() =>
			expect(state.decide).toHaveBeenCalledWith("c", "deny", expect.anything(), undefined, {
				baseUrl: "https://cp.test", orgId: "org-1", sessionId: "cloud-1",
			}),
		);
	});

	it("shows a cloud session's request app-wide when another session is open", () => {
		state.cloud = [{ ...request("c", "cloud-1"), cloud: { baseUrl: "https://cp.test", orgId: "org-1" } }];
		state.routedSessionId = "ao-1";
		renderWith(<GlobalApprovalCard />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(c)");
	});

	it("leaves a teammate's request out of the app-wide card; it shows only in that session", () => {
		state.cloud = [{ ...request("w", "cloud-2"), cloud: { baseUrl: "https://cp.test", orgId: "org-1" }, waitingFor: { name: "Priya Shah" } }];
		state.routedSessionId = "ao-1";
		const { container } = renderWith(<GlobalApprovalCard />);
		expect(container).toBeEmptyDOMElement();
	});

	it("shows the viewer's own request app-wide even behind a teammate's", () => {
		const cloud = { baseUrl: "https://cp.test", orgId: "org-1" };
		state.cloud = [
			{ ...request("theirs", "cloud-2"), cloud, waitingFor: { name: "Priya Shah" } },
			{ ...request("mine", "cloud-3"), cloud },
		];
		state.routedSessionId = "ao-1";
		renderWith(<GlobalApprovalCard />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(mine)");
		expect(screen.queryByText(/more/)).toBeNull();
	});

	it("shows a cloud request app-wide while this machine's daemon is down, and nothing cached from it", () => {
		state.pending = [request("local", "ao-2")];
		state.cloud = [{ ...request("cloudy", "cloud-4"), cloud: { baseUrl: "https://cp.test", orgId: "org-1" } }];
		state.routedSessionId = "ao-1";
		renderWith(<GlobalApprovalCard localReady={false} />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(cloudy)");
		expect(screen.queryByText(/more/)).toBeNull();
	});

	it("says so when an answer arrives after the request had already ended", async () => {
		state.cloud = [{ ...request("late", "cloud-5"), cloud: { baseUrl: "https://cp.test", orgId: "org-1" } }];
		state.routedSessionId = "ao-1";
		state.decide.mockResolvedValueOnce("ended" as never);
		useUiStore.setState({ globalToast: null });
		renderWith(<GlobalApprovalCard />);
		fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
		await waitFor(() => expect(useUiStore.getState().globalToast?.title).toBe("This request had already ended, so your answer wasn't used"));
	});

	it("reads cloud approvals from a reader of its own, not the approval card", () => {
		state.liveReads = 0;
		renderWith(<CloudApprovalReader />);
		expect(state.liveReads).toBeGreaterThan(0);
	});

	it("does not watch a remote host's session in the cloud", () => {
		renderWith(<SessionApprovalDock sessionId="r-1" hostId="box-b" cloudOrgId="org-1" />);
		expect(state.watch).toHaveBeenCalledWith(undefined, "r-1");
	});
});
