import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PendingApproval } from "../../lib/approvals";

const state = vi.hoisted(() => ({
	pending: [] as PendingApproval[],
	routedSessionId: undefined as string | undefined,
	decide: vi.fn(async () => undefined),
	navigate: vi.fn(),
}));

vi.mock("../../lib/approvals", () => ({
	usePendingApprovals: () => ({ data: state.pending }),
	decideApproval: state.decide,
}));
vi.mock("@tanstack/react-router", () => ({
	useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
		select({ matches: [{ params: state.routedSessionId ? { sessionId: state.routedSessionId } : {} }] }),
}));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => state.navigate }));
vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "proj", sessions: [{ id: "ao-2", title: "Book flights" }] }] }),
}));

import { useUiStore } from "../../stores/ui-store";
import { GlobalApprovalCard, SessionApprovalDock } from "./ApprovalHost";

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
		state.routedSessionId = undefined;
		state.decide.mockClear();
		state.navigate.mockClear();
	});

	it("shows a session's oldest request in that session only", async () => {
		state.pending = [request("a", "ao-1"), request("b", "ao-2"), request("c", "ao-1")];
		renderWith(<SessionApprovalDock sessionId="ao-1" />);
		expect(screen.getByTestId("approval-action")).toHaveTextContent("(a)");
		expect(screen.getByText("+1 more")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
		await waitFor(() => expect(state.decide).toHaveBeenCalledWith("a", "allow-once", expect.anything()));
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
		expect(screen.getByText(/Book flights/)).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(state.navigate).toHaveBeenCalledWith("proj", "ao-2");
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

	it("stays out of the way when the only request belongs to the open session", () => {
		state.pending = [request("a", "ao-1")];
		state.routedSessionId = "ao-1";
		const { container } = renderWith(<GlobalApprovalCard />);
		expect(container).toBeEmptyDOMElement();
	});
});
